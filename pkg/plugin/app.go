package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/httpclient"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"github.com/jkroepke/provisioner-reloader/pkg/plugin/observer"
	"github.com/jkroepke/provisioner-reloader/pkg/plugin/transport"
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

	logger log.Logger
	cancel context.CancelFunc
}

type Config struct {
	GrafanaURL string `json:"grafanaURL"`

	AccesscontrolFileWatcher []string `json:"accesscontrolFileWatcher"`
	AlertingFileWatcher      []string `json:"alertingFileWatcher"`
	DashboardsFileWatcher    []string `json:"dashboardsFileWatcher"`
	DatasourcesFileWatcher   []string `json:"datasourcesFileWatcher"`
	PluginsFileWatcher       []string `json:"pluginsFileWatcher"`
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

	cfg := backend.GrafanaConfigFromContext(ctx)
	if !cfg.FeatureToggles().IsEnabled("externalServiceAccounts") {
		app.logger.Error("external service accounts feature is not enabled")

		return nil, fmt.Errorf("external service accounts feature is not enabled")
	}

	saToken, err := cfg.PluginAppClientSecret()
	if err != nil {
		app.logger.Error("failed to get service account token", "error", err)

		return nil, fmt.Errorf("failed to get service account token: %w", err)
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

	httpClient, err := httpclient.New(opts)
	if err != nil {
		return nil, fmt.Errorf("httpclient new: %w", err)
	}

	httpClient.Transport = transport.NewBearerTokenTransport(saToken, httpClient.Transport)

	grafanaURL := "http://localhost:3000"

	if config.GrafanaURL != "" {
		grafanaURL = strings.TrimSuffix(config.GrafanaURL, "/")
	}

	var ob *observer.Observer

	ctx = context.Background()
	ctx, app.cancel = context.WithCancel(ctx)

	for name, filePaths := range map[string][]string{
		"dashboards":     config.DashboardsFileWatcher,
		"datasources":    config.DatasourcesFileWatcher,
		"plugins":        config.PluginsFileWatcher,
		"access-control": config.AccesscontrolFileWatcher,
		"alerting":       config.AlertingFileWatcher,
	} {
		if len(filePaths) > 0 {
			ob, err = observer.New(app.logger, httpClient, "accesscontrol", fmt.Sprintf("%s/api/admin/provisioning/%s/reload", grafanaURL, name), filePaths)
			if err != nil {
				return nil, fmt.Errorf("failed to create observer: %w", err)
			}

			go ob.Run(ctx)
		}
	}

	return &app, nil
}

// Dispose here tells plugin SDK that plugin wants to clean up resources when a new instance
// created.
func (a *App) Dispose() {
	a.cancel()
}

// CheckHealth handles health checks sent from Grafana to the plugin.
func (a *App) CheckHealth(_ context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	return &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: "ok",
	}, nil
}
