// check-facts resolves public sources without AI requests, Telegram or the bot database.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/vaporon4a/movie-helper/internal/facts"
	"github.com/vaporon4a/movie-helper/internal/factsource"
	"github.com/vaporon4a/movie-helper/internal/tmdb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	id := flag.Int64("movie", 0, "TMDB movie ID; zero reads catalog and resolves its first four movies")
	flag.Parse()
	token := os.Getenv("TMDB_API_TOKEN")
	if token == "" {
		return errors.New("TMDB_API_TOKEN required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	httpClient := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client := &tmdb.Client{HTTP: httpClient, BaseURL: "https://api.themoviedb.org/3", Token: token}
	movies, err := diagnosticMovies(ctx, client, *id)
	if err != nil {
		return err
	}
	source := &factsource.Source{HTTP: httpClient, IDs: client, WikidataURL: "https://www.wikidata.org", WikipediaURL: "https://ru.wikipedia.org"}
	for _, movie := range movies {
		episodes, err := source.Episodes(ctx, movie)
		if err != nil {
			return err
		}
		row := struct {
			Movie   facts.Movie
			Count   int
			Samples []facts.Episode
		}{movie, len(episodes), episodes[:min(2, len(episodes))]}
		if err = json.NewEncoder(os.Stdout).Encode(row); err != nil {
			return err
		}
	}
	return nil
}
func diagnosticMovies(ctx context.Context, client *tmdb.Client, id int64) ([]facts.Movie, error) {
	if id > 0 {
		details, err := client.Details(ctx, id)
		if err != nil {
			return nil, err
		}
		return []facts.Movie{{ID: id, Title: details.Title, Year: details.Year}}, nil
	}
	movies, err := client.FactMovies(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	return movies[:min(4, len(movies))], nil
}
