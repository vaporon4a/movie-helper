package clubprofile

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type FilmCatalog interface {
	SearchFilms(context.Context, string) ([]Film, error)
	FilmIdentity(context.Context, int64) (Film, error)
	FilmDetails(context.Context, int64) (Film, error)
}

type FilmEnrichmentRepository interface {
	DueFilmEnrichment(context.Context, time.Time) ([]int64, error)
	SaveFilmEnrichment(context.Context, int64, *Film, time.Time) error
}

type WatchDraftRepository interface {
	CreateWatchDraft(context.Context, int64, int64, WatchInput, time.Time) (WatchDraft, error)
	WatchDraft(context.Context, int64, int64) (WatchDraft, error)
	ChooseWatchFilm(context.Context, int64, int64, int64, time.Time) (WatchDraft, error)
	BindWatchMessage(context.Context, int64, int64, int) error
	ClaimWatchMessage(context.Context, int64, int64, time.Time) (bool, error)
	RetryWatchMessage(context.Context, int64, int64, time.Time) error
	ProfileZone(context.Context, int64) (string, error)
}

type WatchResolverRepository interface {
	CloseWatchCard(context.Context, int64, int) error
	DueWatchDrafts(context.Context, int64, time.Time) ([]WatchDraft, error)
	ClaimWatchDraft(context.Context, int64, time.Time) (bool, error)
	FinishWatchResolution(context.Context, WatchDraft, []Film, string, time.Time) (WatchDraft, error)
	CachedProfileFilm(context.Context, int64) (Film, error)
	WatchCards(context.Context, int64, time.Time) ([]WatchDraft, error)
	WatchCardRendered(context.Context, int64, int) error
}

type WatchHistoryRepository interface {
	ProfileWatches(context.Context, int64) ([]WatchEvent, error)
	ProfileSessions(context.Context, int64) ([]ReviewSession, error)
	ChangeProfileWatch(context.Context, int64, int64, int64, string, string, time.Time) error
	SetAutoReview(context.Context, int64, int64, bool, time.Time) error
}

type WatchRepository interface {
	WatchDraftRepository
	WatchResolverRepository
	WatchHistoryRepository
	FilmEnrichmentRepository
}

type WatchCardPublisher interface {
	PublishWatchCard(context.Context, WatchDraft) error
}

type WatchService struct {
	drafts     WatchDraftRepository
	resolver   WatchResolverRepository
	history    WatchHistoryRepository
	enrichment FilmEnrichmentRepository
	catalog    FilmCatalog
	allowed    map[int64]bool
	log        *slog.Logger
	now        func() time.Time
}

func NewWatchService(repo WatchRepository, catalog FilmCatalog, allowed map[int64]bool, log *slog.Logger, now func() time.Time) (*WatchService, error) {
	if repo == nil || catalog == nil || log == nil || now == nil {
		return nil, ErrInvalid
	}
	return &WatchService{drafts: repo, resolver: repo, history: repo, enrichment: repo, catalog: catalog, allowed: allowed, log: log, now: now}, nil
}

func (s *WatchService) Add(ctx context.Context, op, chat int64, text string) (WatchDraft, error) {
	if !s.allowed[chat] {
		return WatchDraft{}, ErrInvalid
	}
	zone, err := s.drafts.ProfileZone(ctx, chat)
	if err != nil {
		return WatchDraft{}, err
	}
	in, err := ParseWatchInput(text, zone, s.now())
	if err != nil {
		return WatchDraft{}, err
	}
	d, err := s.drafts.CreateWatchDraft(ctx, op, chat, in, s.now())
	if err != nil {
		return d, err
	}
	return s.resolve(ctx, d)
}

func (s *WatchService) Choose(ctx context.Context, chat, id, film int64) (WatchDraft, error) {
	if !s.allowed[chat] {
		return WatchDraft{}, ErrInvalid
	}
	d, err := s.drafts.ChooseWatchFilm(ctx, chat, id, film, s.now())
	if err != nil {
		return d, err
	}
	return s.resolve(ctx, d)
}

func (s *WatchService) BindMessage(ctx context.Context, chat, id int64, message int) error {
	if !s.allowed[chat] {
		return ErrInvalid
	}
	return s.drafts.BindWatchMessage(ctx, chat, id, message)
}

func (s *WatchService) CloseMessage(ctx context.Context, chat int64, message int) error {
	if !s.allowed[chat] || message <= 0 {
		return ErrInvalid
	}
	return s.resolver.CloseWatchCard(ctx, chat, message)
}

func (s *WatchService) ClaimMessage(ctx context.Context, chat, id int64) (bool, error) {
	if !s.allowed[chat] {
		return false, ErrInvalid
	}
	return s.drafts.ClaimWatchMessage(ctx, chat, id, s.now())
}

func (s *WatchService) resolve(ctx context.Context, d WatchDraft) (WatchDraft, error) {
	if d.State != "pending" {
		return d, nil
	}
	claimed, err := s.resolver.ClaimWatchDraft(ctx, d.ID, s.now())
	if err != nil || !claimed {
		return d, err
	}
	films, err := s.lookup(ctx, d.Input)
	reason := ""
	if err != nil {
		reason = "catalog_unavailable"
		films = nil
	}
	// Resolution retries are durable; a network failure does not discard the original request.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.resolver.FinishWatchResolution(persist, d, films, reason, s.now())
}

func (s *WatchService) lookup(ctx context.Context, in WatchInput) ([]Film, error) {
	request, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if in.TMDBID == 0 {
		return s.catalog.SearchFilms(request, in.Query)
	}
	f, err := s.catalog.FilmIdentity(request, in.TMDBID)
	if err != nil {
		f, err = s.resolver.CachedProfileFilm(ctx, in.TMDBID)
	}
	if err != nil {
		return nil, err
	}
	if f.TMDBID != in.TMDBID || f.Title == "" {
		return nil, ErrInvalid
	}
	return []Film{f}, nil
}

func (s *WatchService) RetryMessage(ctx context.Context, chat, id int64, after time.Duration) error {
	if !s.allowed[chat] {
		return ErrInvalid
	}
	return s.drafts.RetryWatchMessage(ctx, chat, id, s.now().Add(max(after, time.Second)))
}

func (s *WatchService) Tick(ctx context.Context, editor WatchCardPublisher) error {
	var errs []error
	for chat, allowed := range s.allowed {
		if !allowed {
			continue
		}
		drafts, err := s.resolver.DueWatchDrafts(ctx, chat, s.now())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, d := range drafts {
			if _, e := s.resolve(ctx, d); e != nil {
				errs = append(errs, e)
			}
		}
		errs = append(errs, s.refreshCards(ctx, chat, editor))
	}
	return errors.Join(errs...)
}

func (s *WatchService) refreshCards(ctx context.Context, chat int64, editor WatchCardPublisher) error {
	var errs []error
	cards, err := s.resolver.WatchCards(ctx, chat, s.now())
	if err != nil {
		return err
	}
	for _, d := range cards {
		if !s.allowed[d.ChatID] {
			continue
		}
		if e := editor.PublishWatchCard(ctx, d); e != nil {
			failure, ok := errors.AsType[*DeliveryError](e)
			if !ok || (failure.Kind != "permanent" && failure.Kind != "forbidden") {
				continue
			}
			s.log.WarnContext(ctx, "watch card unavailable", "draft_id", d.ID, "reason", failure.Reason)
		}
		if e := s.resolver.WatchCardRendered(ctx, d.ID, d.Version); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

func (s *WatchService) Enrich(ctx context.Context) error {
	ids, err := s.enrichment.DueFilmEnrichment(ctx, s.now())
	if err != nil {
		return err
	}
	for _, id := range ids {
		request, cancel := context.WithTimeout(ctx, 12*time.Second)
		f, e := s.catalog.FilmDetails(request, id)
		cancel()
		var film *Film
		if e == nil && f.TMDBID == id && f.Title != "" && f.Source == "tmdb" {
			film = &f
		}
		if err = s.enrichment.SaveFilmEnrichment(ctx, id, film, s.now()); err != nil {
			return err
		}
	}
	return nil
}

func (s *WatchService) Run(ctx context.Context, editor WatchCardPublisher) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.Tick(ctx, editor); err != nil && !errors.Is(err, context.Canceled) {
			s.log.WarnContext(ctx, "watch resolver failed", "reason", "storage_or_validation")
		}
		if err := s.Enrich(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.log.WarnContext(ctx, "film enrichment failed", "reason", "storage")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *WatchService) History(ctx context.Context, chat int64) ([]WatchEvent, []ReviewSession, error) {
	if !s.allowed[chat] {
		return nil, nil, ErrInvalid
	}
	w, err := s.history.ProfileWatches(ctx, chat)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := s.history.ProfileSessions(ctx, chat)
	return w, sessions, err
}

func (s *WatchService) Change(ctx context.Context, op, chat, id int64, action, value string) error {
	if !s.allowed[chat] {
		return ErrInvalid
	}
	return s.history.ChangeProfileWatch(ctx, op, chat, id, action, value, s.now())
}

func (s *WatchService) AutoReview(ctx context.Context, op, chat int64, enabled bool) error {
	if !s.allowed[chat] {
		return ErrInvalid
	}
	return s.history.SetAutoReview(ctx, op, chat, enabled, s.now())
}
