//go:build !server

package app

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"

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
	available            atomic.Bool
	requestAuthorization sync.Once
}

// NewSafeNotificationsService creates the wrapped notifications service.
func NewSafeNotificationsService(onActivate func()) *SafeNotificationsService {
	s := &SafeNotificationsService{inner: notifications.New()}
	s.inner.OnNotificationResponse(func(result notifications.NotificationResult) {
		if result.Error == nil && result.Response.ActionIdentifier == notifications.DefaultActionIdentifier && onActivate != nil {
			onActivate()
		}
	})
	return s
}

// ServiceStartup implements application.ServiceStartup. Failures only disable
// notifications; the application must keep starting.
func (s *SafeNotificationsService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := s.inner.ServiceStartup(ctx, options); err != nil {
		println("Ally notifications disabled:", err.Error())
		return nil
	}
	s.available.Store(true)
	return nil
}

// ServiceShutdown implements application.ServiceShutdown.
func (s *SafeNotificationsService) ServiceShutdown() error {
	return s.inner.ServiceShutdown()
}

// SendNotification implements completionNotifier and no-ops when the platform
// backend failed to start.
func (s *SafeNotificationsService) SendNotification(options completionNotification) error {
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
