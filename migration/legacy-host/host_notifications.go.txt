//go:build !server

package app

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// SafeNotificationsService wraps the Wails notifications service so platform
// startup failures (macOS unbundled binary without a bundle identifier, Linux
// without a session bus) degrade to silent no-ops instead of aborting the
// whole application startup — Wails aborts App.Run when any service Startup
// returns an error, which would make the completion sound a startup
// hard-dependency on every platform that cannot honour it.
type SafeNotificationsService struct {
	inner                *notifications.NotificationService
	app                  *App
	available            atomic.Bool
	requestAuthorization sync.Once
	lastErrorAt          atomic.Int64
}

// NewSafeNotificationsService creates the wrapped notifications service.
func NewSafeNotificationsService(app *App) *SafeNotificationsService {
	s := &SafeNotificationsService{inner: notifications.New(), app: app}
	s.inner.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error != nil {
			s.logFailure(result.Error)
		} else if result.Response.ActionIdentifier == notifications.DefaultActionIdentifier {
			app.showMainWindow()
		}
	})
	return s
}

// ServiceStartup implements application.ServiceStartup. Failures only disable
// notifications; the application must keep starting.
func (s *SafeNotificationsService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := s.inner.ServiceStartup(ctx, options); err != nil {
		s.app.logAppError("notifications disabled", "error", err)
		return nil
	}
	s.available.Store(true)
	return nil
}

// ServiceShutdown implements application.ServiceShutdown.
func (s *SafeNotificationsService) ServiceShutdown() error {
	s.available.Store(false)
	return s.inner.ServiceShutdown()
}

// SendNotification implements completionNotifier and no-ops when the platform
// backend failed to start.
func (s *SafeNotificationsService) SendNotification(options completionNotification) (err error) {
	defer func() {
		if err != nil {
			s.logFailure(err)
		}
	}()
	if !s.available.Load() {
		return nil
	}
	if runtime.GOOS == "darwin" {
		// Request permission only when a notification is first needed. This
		// method runs in the completion goroutine, never blocking app startup.
		authorized, err := s.inner.CheckNotificationAuthorization()
		if err != nil {
			return err
		}
		if !authorized {
			s.requestAuthorization.Do(func() {
				authorized, err = s.inner.RequestNotificationAuthorization()
			})
			if err != nil {
				return err
			}
			if !authorized {
				return nil
			}
		}
	}
	opts := notifications.NotificationOptions{ID: options.ID, Title: options.Title, Body: options.Body}
	if options.Sound != "" {
		opts.Sound = &notifications.NotificationSound{Name: options.Sound}
	}
	return s.inner.SendNotification(opts)
}

// An unavailable notification daemon must not flood the error log on each run.
func (s *SafeNotificationsService) logFailure(err error) {
	now := time.Now().UnixNano()
	last := s.lastErrorAt.Load()
	if last != 0 && now-last < int64(time.Minute) {
		return
	}
	if s.lastErrorAt.CompareAndSwap(last, now) {
		s.app.logAppError("notification failed", "error", err)
	}
}
