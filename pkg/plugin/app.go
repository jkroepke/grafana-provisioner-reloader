package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/httpclient"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

// Make sure App implements required interfaces. This is important to do
// since otherwise we will only get a not implemented error response from plugin in
// runtime. Plugin should not implement all these interfaces - only those which are
// required for a particular task.
var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

// App is an example app backend plugin which can respond to data queries.
type App struct {
	backend.CallResourceHandler

	httpClient     *http.Client
	disposeCh      chan struct{}
	logger         log.Logger
	healthStatus   backend.HealthStatus
	healthStatusMu sync.RWMutex
	saToken        string
	grafanaURL     string
}

var healthStatusMessage = map[backend.HealthStatus]string{
	backend.HealthStatusOk:    "ok",
	backend.HealthStatusError: "error",
}

type Config struct {
	FSWatcher  []string `json:"fsWatcher"`
	GrafanaURL string   `json:"grafanaURL"`
}

// NewApp creates a new example *App instance.
func NewApp(ctx context.Context, settings backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	var (
		app App
		err error
	)

	// Use a httpadapter (provided by the SDK) for resource calls. This allows us
	// to use a *http.ServeMux for resource calls, so we can map multiple routes
	// to CallResource without having to implement extra logic.
	mux := http.NewServeMux()
	app.registerRoutes(mux)
	app.CallResourceHandler = httpadapter.New(mux)

	app.logger = log.DefaultLogger.FromContext(ctx)
	app.disposeCh = make(chan struct{})

	cfg := backend.GrafanaConfigFromContext(ctx)

	app.saToken, err = cfg.PluginAppClientSecret()
	if err != nil {
		app.logger.Error("failed to get service account token", "error", err)

		return nil, fmt.Errorf("failed to get service account token: %w", err)
	}

	if !cfg.FeatureToggles().IsEnabled("externalServiceAccounts") {
		app.logger.Error("external service accounts feature is not enabled")

		return nil, fmt.Errorf("external service accounts feature is not enabled")
	}

	var config Config

	if err := json.Unmarshal(settings.JSONData, &config); err != nil {
		app.logger.Error("error in unmarshalling plugin settings", "error", err)

		return nil, fmt.Errorf("error in unmarshalling plugin settings: %w", err)
	}

	opts, err := settings.HTTPClientOptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("http client options: %w", err)
	}

	app.httpClient, err = httpclient.New(opts)
	if err != nil {
		return nil, fmt.Errorf("httpclient new: %w", err)
	}

	app.grafanaURL = "http://localhost:3000"

	if config.GrafanaURL == "" {
		app.grafanaURL = strings.TrimSuffix(config.GrafanaURL, "/")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		app.logger.Error("failed to create watcher", "error", err)

		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	for _, path := range config.FSWatcher {
		if err := watcher.Add(path); err != nil {
			app.logger.Error("failed to add path to watcher", "path", path, "error", err)

			return nil, fmt.Errorf("failed to add path to watcher: %w", err)
		}
	}

	go app.run(watcher)

	return &app, nil
}

// Dispose here tells plugin SDK that plugin wants to clean up resources when a new instance
// created.
func (a *App) Dispose() {
	if _, ok := <-a.disposeCh; !ok {
		// The dispose channel is already closed, so the goroutine has stopped.
		return
	}

	// Signal the running goroutine to stop.
	a.disposeCh <- struct{}{}

	// Wait for the goroutine to stop.
	select {
	case <-a.disposeCh:
		// The goroutine has stopped.
	case <-time.After(5 * time.Second):
		// The goroutine has not stopped after 5 seconds. Log an error.
		a.logger.Error("failed to stop the plugin")
	}
}

// CheckHealth handles health checks sent from Grafana to the plugin.
func (a *App) CheckHealth(_ context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	a.healthStatusMu.RLock()
	defer a.healthStatusMu.RUnlock()

	return &backend.CheckHealthResult{
		Status:  a.healthStatus,
		Message: healthStatusMessage[a.healthStatus],
	}, nil
}
