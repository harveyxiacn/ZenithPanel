package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	backupsvc "github.com/harveyxiacn/ZenithPanel/backend/internal/service/backup"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/notify"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/scheduler"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/sub"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/webserver"
)

type backupReconcileStatus struct {
	State        string   `json:"state"`
	DataRestored bool     `json:"data_restored"`
	Errors       []string `json:"errors,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

type backupRuntimeCoordinator struct {
	opMu   sync.Mutex
	stateM sync.RWMutex
	state  backupReconcileStatus
	sched  *scheduler.Scheduler
	xray   *proxy.XrayManager
	sing   *proxy.SingboxManager
}

func newBackupRuntimeCoordinator(sched *scheduler.Scheduler, xm *proxy.XrayManager, sm *proxy.SingboxManager) *backupRuntimeCoordinator {
	return &backupRuntimeCoordinator{
		state: backupReconcileStatus{State: "idle"},
		sched: sched,
		xray:  xm,
		sing:  sm,
	}
}

func (b *backupRuntimeCoordinator) setStatus(status backupReconcileStatus) {
	b.stateM.Lock()
	b.state = status
	b.stateM.Unlock()
}

func (b *backupRuntimeCoordinator) status() backupReconcileStatus {
	b.stateM.RLock()
	defer b.stateM.RUnlock()
	status := b.state
	status.Errors = append([]string(nil), b.state.Errors...)
	return status
}

// reconcileLocked replays every runtime consumer of restored DB state. Caller
// holds opMu so two retries cannot stop/start proxy engines concurrently.
func (b *backupRuntimeCoordinator) reconcileLocked() backupReconcileStatus {
	b.setStatus(backupReconcileStatus{State: "running", DataRestored: true, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	sub.InvalidateSubCache()
	var reconcileErrors []string
	if b.sched != nil {
		if err := b.sched.ReloadFromDB(); err != nil {
			reconcileErrors = append(reconcileErrors, "scheduler: "+err.Error())
		}
	}
	if manager := webserver.Get(); manager != nil {
		if err := manager.Reload(); err != nil {
			reconcileErrors = append(reconcileErrors, "webserver: "+err.Error())
		}
	}
	notify.TriggerReload()
	if err := reconcileProxyRuntime(b.xray, b.sing); err != nil {
		reconcileErrors = append(reconcileErrors, "proxy: "+err.Error())
	}
	status := backupReconcileStatus{
		State:        "succeeded",
		DataRestored: true,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if len(reconcileErrors) > 0 {
		status.State = "failed"
		status.Errors = reconcileErrors
	}
	b.setStatus(status)
	return status
}

func registerBackupRoutes(g *gin.RouterGroup, sched *scheduler.Scheduler, xm *proxy.XrayManager, sm *proxy.SingboxManager) {
	coordinator := newBackupRuntimeCoordinator(sched, xm, sm)

	g.GET("/admin/backup/export", func(c *gin.Context) {
		filename := fmt.Sprintf("zenithpanel-backup-%s.zip", time.Now().UTC().Format("20060102-150405"))
		c.Header("Content-Type", "application/zip")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		c.Header("Cache-Control", "no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		counts, err := backupsvc.Export(c.Writer)
		if err != nil {
			log.Printf("Backup export failed: %v", err)
			c.Status(http.StatusInternalServerError)
			return
		}
		recordAudit(c, "backup.export", fmt.Sprintf("items=%v", counts))
	})

	g.POST("/admin/backup/restore", func(c *gin.Context) {
		const maxBackupBytes = 16 * 1024 * 1024
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBackupBytes)
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Failed to read backup payload (max 16 MB)"})
			return
		}
		if len(body) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Empty backup payload"})
			return
		}

		coordinator.opMu.Lock()
		defer coordinator.opMu.Unlock()
		counts, err := backupsvc.Restore(body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": fmt.Sprintf("Restore failed: %v", err)})
			return
		}
		status := coordinator.reconcileLocked()
		recordAudit(c, "backup.restore", fmt.Sprintf("items=%v reconcile=%s", counts, status.State))
		if status.State == "failed" {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  "Backup data restored, but runtime reconciliation failed",
				"data": gin.H{"counts": counts, "reconcile": status},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Restored", "data": gin.H{"counts": counts, "reconcile": status}})
	})

	g.GET("/admin/backup/reconcile", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": coordinator.status()})
	})

	g.POST("/admin/backup/reconcile", func(c *gin.Context) {
		coordinator.opMu.Lock()
		defer coordinator.opMu.Unlock()
		if !coordinator.status().DataRestored {
			c.JSON(http.StatusConflict, gin.H{"code": 409, "msg": "No restored backup is awaiting reconciliation"})
			return
		}
		status := coordinator.reconcileLocked()
		if status.State == "failed" {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Runtime reconciliation failed", "data": status})
			return
		}
		recordAudit(c, "backup.reconcile", "succeeded")
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Runtime reconciled", "data": status})
	})
}
