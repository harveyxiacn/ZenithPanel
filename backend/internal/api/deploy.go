package api

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/deploy"
)

// deployOrchestrator is lazily initialized on first use so tests and
// non-deploy code paths don't pay the cost. It depends on config.DB being
// ready, which is true after InitDB.
var (
	deployOrchestratorOnce sync.Once
	deployOrchestrator     *deploy.Orchestrator
)

// smartDeployCertRoot is the on-disk root for cert files provisioned by
// Smart Deploy. Match the path the existing cert service uses so audits
// find everything in one place.
const smartDeployCertRoot = "/opt/zenithpanel/data/certs"

func getOrchestrator() *deploy.Orchestrator {
	deployOrchestratorOnce.Do(func() {
		// ACME is not yet wired in Phase 1. The cert manager returns
		// ErrACMENotConfigured when Mode=acme is requested without a
		// client; the orchestrator surfaces that as a deployment failure.
		mgr := cert.NewManager(smartDeployCertRoot, nil)
		deployOrchestrator = deploy.NewOrchestrator(config.DB, mgr)
	})
	return deployOrchestrator
}

// RegisterDeployRoutes attaches the /api/v1/deploy/* endpoints to an
// already-auth'd route group. Called from SetupRoutes.
func RegisterDeployRoutes(authGroup *gin.RouterGroup) {
	g := authGroup.Group("/deploy")
	g.POST("/probe", handleDeployProbe)
	g.POST("/preview", handleDeployPreview)
	g.POST("/apply", handleDeployApply)
	g.GET("", handleDeployList)
	g.GET("/:id", handleDeployGet)
	g.POST("/:id/rollback", handleDeployRollback)
	g.GET("/:id/clients", handleDeployClients)
}

// ─────────────────────────────────────────────────────────────────────────
// Request / response shapes
// ─────────────────────────────────────────────────────────────────────────

type deployRequest struct {
	PresetID      string         `json:"preset_id"`
	Domain        string         `json:"domain,omitempty"`
	Email         string         `json:"email,omitempty"`
	PortOverride  int            `json:"port_override,omitempty"`
	RealityTarget string         `json:"reality_target,omitempty"`
	Options       map[string]any `json:"options,omitempty"`
}

func (r deployRequest) toInput() deploy.Input {
	return deploy.Input{
		Domain:        r.Domain,
		Email:         r.Email,
		PortOverride:  r.PortOverride,
		RealityTarget: r.RealityTarget,
		Options:       r.Options,
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Handlers
// ─────────────────────────────────────────────────────────────────────────

func handleDeployProbe(c *gin.Context) {
	res := getOrchestrator().Probe(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": res})
}

func handleDeployPreview(c *gin.Context) {
	var req deployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": fmt.Sprintf("Invalid body: %v", err)})
		return
	}
	if req.PresetID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "preset_id is required"})
		return
	}
	plan, probe, err := getOrchestrator().Preview(c.Request.Context(), req.PresetID, req.toInput())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
		"plan":  plan,
		"probe": probe,
	}})
}

func handleDeployApply(c *gin.Context) {
	var req deployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": fmt.Sprintf("Invalid body: %v", err)})
		return
	}
	if req.PresetID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "preset_id is required"})
		return
	}
	dep, err := getOrchestrator().Apply(c.Request.Context(), req.PresetID, req.toInput())
	if err != nil {
		status := http.StatusInternalServerError
		if dep == nil {
			// Apply refused before creating a deployment record (e.g.
			// not-root check). Treat as a bad-request-like failure.
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"code": status, "msg": err.Error(), "data": dep})
		recordAudit(c, "deploy.apply.failed", req.PresetID+": "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": dep})
	recordAudit(c, "deploy.apply", fmt.Sprintf("%s (id=%d)", req.PresetID, dep.ID))
}

func handleDeployList(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}

	var deployments []model.Deployment
	if err := config.DB.Order("id DESC").Limit(limit).Offset(offset).Find(&deployments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": deployments})
}

func handleDeployGet(c *gin.Context) {
	id, ok := parseUintID(c)
	if !ok {
		return
	}
	var dep model.Deployment
	if err := config.DB.First(&dep, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "deployment not found"})
		return
	}
	var ops []model.DeploymentOp
	config.DB.Where("deployment_id = ?", id).Order("sequence").Find(&ops)
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
		"deployment": dep,
		"ops":        ops,
	}})
}

func handleDeployRollback(c *gin.Context) {
	id, ok := parseUintID(c)
	if !ok {
		return
	}
	dep, err := getOrchestrator().Rollback(c.Request.Context(), uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": dep})
	recordAudit(c, "deploy.rollback", fmt.Sprintf("id=%d", dep.ID))
}

// handleDeployClients returns the subscription link and per-client configs
// for a successful deployment. Reuses the existing subscription machinery
// by looking up the inbounds this deployment created and generating a
// compact subscription payload keyed off the deployment id.
func handleDeployClients(c *gin.Context) {
	id, ok := parseUintID(c)
	if !ok {
		return
	}
	var dep model.Deployment
	if err := config.DB.First(&dep, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "deployment not found"})
		return
	}
	if dep.Status != model.DeployStatusSucceeded {
		c.JSON(http.StatusConflict, gin.H{"code": 409, "msg": "deployment is not in succeeded state"})
		return
	}

	// For Phase 1, return the inbound IDs and a note directing the user to
	// the existing inbound-detail page for now. Full per-deployment
	// subscription wiring lands alongside Task 9 (frontend wizard) when the
	// frontend generates the subscription URL from the inbound ids.
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": gin.H{
		"deployment_id": dep.ID,
		"inbound_ids":   dep.InboundIDs,
		"note":          "Use /api/v1/sub or the inbound detail page to fetch client configs for these inbounds.",
	}})
}
