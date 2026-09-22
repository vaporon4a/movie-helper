package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"golang.org/x/image/draw"
)

func (c *Editor) image(ctx context.Context, raw string) (*Inline, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "i.redd.it" || u.User != nil {
		return nil, errors.New("unsupported image host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := client.Do(req)
	if err != nil {
		return nil, errors.New("image unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, errors.New("image status")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return nil, errors.New("image too large")
	}
	originalBytes := len(data)
	data, mime, width, height, err := analysisImage(data)
	if err != nil {
		return nil, err
	}
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info("meme analysis image", "scope", c.Scope, "original_bytes", originalBytes, "analysis_bytes", len(data), "width", width, "height", height)
	return &Inline{MIME: mime, Data: base64.StdEncoding.EncodeToString(data)}, nil
}

func analysisImage(data []byte) ([]byte, string, int, int, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, "", 0, 0, errors.New("unsupported image")
	}
	// Bound decoded memory, not just compressed bytes, before allocating pixels.
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil, "", 0, 0, errors.New("image dimensions too large")
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, errors.New("invalid image")
	}
	w, h := cfg.Width, cfg.Height
	if w > 1280 || h > 1280 {
		if w >= h {
			h = max(1, h*1280/w)
			w = 1280
		} else {
			w = max(1, w*1280/h)
			h = 1280
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), im, im.Bounds(), draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, "", 0, 0, err
	}
	// Small PNGs often compress better than JPEG; never inflate them needlessly.
	if w == cfg.Width && h == cfg.Height && buf.Len() >= len(data) {
		return data, "image/" + format, w, h, nil
	}
	return buf.Bytes(), "image/jpeg", w, h, nil
}
