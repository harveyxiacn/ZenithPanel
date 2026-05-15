package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api/middleware"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/apitoken"
)

var tokenNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.\-]{1,64}$`)

// registerAPITokenRoutes wires the /admin/api-tokens CRUD onto the protected
// authGroup. Called from SetupRoutes once it has built that group.
func registerAPITokenRoutes(g *gin.RouterGroup) {
	g.GET("/admin/api-tokens", listAPITokens)
	g.POST("/admin/api-tokens", createAPIToken)
	g.DELETE("/admin/api-tokens/:id", revokeAPIToken)

	// Local-only self-service: only reachable when the request landed on the
	// unix socket. The handler double-checks `trusted_local`, but routing
	// through authGroup still gives us the same audit & logging plumbing.
	g.POST("/admin/api-tokens/bootstrap", bootstrapAPIToken)
}

func listAPITokens(c *gin.Context) {
	if !middleware.HasScope(c, "admin") {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Scope 'admin' required"})
		return
	}
	var rows []model.ApiToken
	if err := config.DB.Order("id DESC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to list tokens"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "ok", "data": rows})
}

type createTokenReq struct {
	Name          string `json:"name"`
	Scopes        string `json:"scopes"`
	ExpiresInDays int    `json:"expires_in_days"`
}

func createAPIToken(c *gin.Context) {
	if !middleware.HasScope(c, "admin") {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Scope 'admin' required"})
		return
	}
	var req createTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid parameters"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !tokenNameRe.MatchString(req.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Name must match [a-zA-Z0-9_.-]{1,64}"})
		return
	}
	scopes := strings.TrimSpace(req.Scopes)
	if scopes == "" {
		scopes = "*"
	}

	row, plaintext, err := mintToken(req.Name, scopes, req.ExpiresInDays)
	if err != nil {
		if err == errTokenNameTaken {
			c.JSON(http.StatusConflict, gin.H{"code": 409, "msg": "Token name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to create token: " + err.Error()})
		return
	}
	recordAudit(c, "api_token.create", "name="+row.Name+" scopes="+row.Scopes)
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "Token created. Copy it now — it will not be shown again.",
		"data": gin.H{
			"id":         row.ID,
			"name":       row.Name,
			"scopes":     row.Scopes,
			"expires_at": row.ExpiresAt,
			"token":      plaintext,
		},
	})
}

func revokeAPIToken(c *gin.Context) {
	if !middleware.HasScope(c, "admin") {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "Scope 'admin' required"})
		return
	}
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid id"})
		return
	}
	var row model.ApiToken
	if err := config.DB.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "Token not found"})
		return
	}
	row.Revoked = true
	if err := config.DB.Save(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to revoke"})
		return
	}
	recordAudit(c, "api_token.revoke", "name="+row.Name)
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "revoked"})
}

func bootstrapAPIToken(c *gin.Context) {
	if !c.GetBool("trusted_local") {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "bootstrap only available on unix socket"})
		return
	}
	name := "local-root-" + strconv.FormatInt(time.Now().Unix(), 10)
	row, plaintext, err := mintToken(name, "*", 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "Failed to mint: " + err.Error()})
		return
	}
	recordAudit(c, "api_token.bootstrap", "name="+row.Name)
	c.JSON(http.StatusOK, gin.H{
		"code": 200, "msg": "ok",
		"data": gin.H{"id": row.ID, "name": row.Name, "scopes": row.Scopes, "token": plaintext},
	})
}

var errTokenNameTaken = &simpleErr{"token name taken"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

// mintToken creates a row + plaintext; returns the persisted row and the
// plaintext that must be shown to the caller exactly once.
func mintToken(name, scopes string, expiresInDays int) (*model.ApiToken, string, error) {
	var existing model.ApiToken
	if err := config.DB.Where("name = ?", name).First(&existing).Error; err == nil {
		return nil, "", errTokenNameTaken
	}
	plaintext, hash, err := apitoken.Generate()
	if err != nil {
		return nil, "", err
	}
	row := &model.ApiToken{
		Name:      name,
		TokenHash: hash,
		Scopes:    scopes,
	}
	if expiresInDays > 0 {
		row.ExpiresAt = time.Now().AddDate(0, 0, expiresInDays).Unix()
	}
	if err := config.DB.Create(row).Error; err != nil {
		return nil, "", err
	}
	return row, plaintext, nil
}
