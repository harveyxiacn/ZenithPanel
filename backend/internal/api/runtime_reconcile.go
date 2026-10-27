package api

import (
	"context"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"time"
)

// Restore, manual Apply and deployment use the same serialized transition.
func reconcileProxyRuntime(xm *proxy.XrayManager, sm *proxy.SingboxManager) error {
	if xm == nil || sm == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return proxy.ApplyRuntime(ctx, xm, sm, "auto")
}
