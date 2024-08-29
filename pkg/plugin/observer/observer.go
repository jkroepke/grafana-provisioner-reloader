package observer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

type Observer struct {
	endpoint string

	logger     log.Logger
	httpClient *http.Client

	watcher *fsnotify.Watcher

	reloadCh chan struct{}
	errCh    chan error
}

func New(logger log.Logger, httpClient *http.Client, name, endpoint string, paths []string) (*Observer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	for _, path := range paths {
		if err = watcher.Add(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				logger.Warn("failed to add path to watcher", "path", path, "err", err)
				continue
			}

			return nil, fmt.Errorf("failed to add path %q to watcher: %w", path, err)
		}
	}

	return &Observer{
		logger:     logger.With("observer", name),
		httpClient: httpClient,
		endpoint:   endpoint,
		watcher:    watcher,
		reloadCh:   make(chan struct{}, 50),
	}, nil
}

func (o *Observer) Run(ctx context.Context) {
	o.logger.Debug("watching files", "files", o.watcher.WatchList())

	go o.reload(ctx)

	for {
		select {
		case <-ctx.Done():
			if err := o.watcher.Close(); err != nil {
				o.logger.Error("failed to close watcher", "error", err)
			}

			close(o.reloadCh)

			return
		case event, ok := <-o.watcher.Events:
			if !ok {
				return
			}

			if !event.Has(fsnotify.Write) {
				continue
			}

			o.logger.Debug("config file changed", "file", event.Name)
			o.reloadCh <- struct{}{}
		case err, ok := <-o.watcher.Errors:
			if !ok {
				return
			}

			o.logger.Error("watcher error", "error", err)
		}
	}
}

func (o *Observer) reload(ctx context.Context) {
	for {
		time.Sleep(30 * time.Second)

		select {
		case <-ctx.Done():
			return
		case _, ok := <-o.reloadCh:
			if !ok {
				return
			}

			// if we have more in queue, drain channel and reload only once
			for len(o.reloadCh) > 0 {
				<-o.reloadCh
			}

			o.logger.Debug("reloading provisioned config")

			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, o.endpoint, nil)
			if err != nil {
				o.logger.Error("failed to create request", "error", err)

				continue
			}

			res, err := o.httpClient.Do(req)

			if err != nil {
				o.logger.Error("failed to send request", "error", err)
			} else if err = checkResponse(res); err != nil {
				o.logger.Error("failed to reload provisioned config", "error", err)
			}
		default:
			// no reload request in queue
		}
	}
}

func checkResponse(res *http.Response) error {
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if err = res.Body.Close(); err != nil {
		return fmt.Errorf("failed to close response body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d, body: %s", res.StatusCode, resBody)
	}

	return nil
}
