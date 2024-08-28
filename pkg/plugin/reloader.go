package plugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func (a *App) run(watcher *fsnotify.Watcher) {
	defer func(watcher *fsnotify.Watcher) {
		if err := watcher.Close(); err != nil {
			a.logger.Error("failed to close watcher", "error", err)
		}

		a.healthStatusMu.Lock()
		a.healthStatus = backend.HealthStatusError // Set health status to error if the goroutine stops.
		a.healthStatusMu.Unlock()

		close(a.disposeCh)
	}(watcher)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !event.Has(fsnotify.Write) {
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.grafanaURL+"/api/admin/provisioning/dashboards/reload", nil)
			if err != nil {
				cancel()

				a.logger.Error("failed to create request", "error", err)

				continue
			}

			req.Header.Set(backend.OAuthIdentityTokenHeaderName, "Bearer "+a.saToken)

			res, err := a.httpClient.Do(req)
			cancel()

			if err != nil {
				a.logger.Error("failed to send request", "error", err)

				continue
			}

			if err = checkResponse(res); err != nil {
				a.logger.Error("failed to reload provisioned config", "error", err)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}

			a.logger.Error("watcher error", "error", err)
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
