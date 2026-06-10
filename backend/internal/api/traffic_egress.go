package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/traffic"
)

// egressConfigKeys are the settings exposed (read + write) by the egress config
// endpoint. Writes are restricted to this allowlist.
var egressConfigKeys = []string{
	traffic.SettingEgressEnabled,
	traffic.SettingEgressRetentionDays,
	traffic.SettingEgressHourlyRetentionDays,
	traffic.SettingEgressASNEnabled,
	traffic.SettingEgressRDNSEnabled,
	traffic.SettingEgressSocketSampler,
	traffic.SettingEgressXrayAccessPath,
	traffic.SettingEgressInstanceMap,
	traffic.SettingEgressPruneHour,
}

func registerEgressRoutes(rg *gin.RouterGroup, eg *traffic.EgressCollector) {
	guard := func(c *gin.Context) bool {
		if eg == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "Egress collector not initialized"})
			return false
		}
		return true
	}

	rg.GET("/traffic/egress", func(c *gin.Context) {
		if !guard(c) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": eg.Query(egressFilterFromQuery(c))})
	})

	rg.GET("/traffic/egress/summary", func(c *gin.Context) {
		if !guard(c) {
			return
		}
		groupBy := strings.TrimSpace(c.DefaultQuery("group_by", "domain"))
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": eg.Summary(egressFilterFromQuery(c), groupBy)})
	})

	rg.GET("/traffic/egress/series", func(c *gin.Context) {
		if !guard(c) {
			return
		}
		split := c.Query("split") == "instance"
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": eg.Series(egressFilterFromQuery(c), split)})
	})

	rg.GET("/traffic/egress/coverage", func(c *gin.Context) {
		if !guard(c) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": eg.Coverage()})
	})

	rg.GET("/traffic/egress/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": readEgressConfig()})
	})

	rg.PUT("/traffic/egress/config", func(c *gin.Context) {
		var body map[string]string
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Invalid request body"})
			return
		}
		allowed := map[string]bool{}
		for _, k := range egressConfigKeys {
			allowed[k] = true
		}
		var changed []string
		for k, v := range body {
			if !allowed[k] {
				continue
			}
			if err := config.SetSetting(k, v); err == nil {
				changed = append(changed, k)
			}
		}
		sort.Strings(changed)
		recordAudit(c, "traffic.egress.config", "updated: "+strings.Join(changed, ", "))
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "Success", "data": readEgressConfig()})
	})
}

func readEgressConfig() gin.H {
	out := gin.H{}
	for _, k := range egressConfigKeys {
		out[k] = config.GetSetting(k)
	}
	return out
}

// egressFilterFromQuery builds an EgressFilter from query params, defaulting to
// the last 6 hours when no window is given.
func egressFilterFromQuery(c *gin.Context) traffic.EgressFilter {
	now := time.Now().Unix()
	f := traffic.EgressFilter{Start: now - 6*3600, End: now}
	if v, ok := parseQueryInt64(c.Query("start")); ok {
		f.Start = v
	}
	if v, ok := parseQueryInt64(c.Query("end")); ok {
		f.End = v
	}
	f.Instance = strings.TrimSpace(c.Query("instance"))
	f.User = strings.TrimSpace(c.Query("user"))
	f.Direction = strings.TrimSpace(c.Query("direction"))
	if n, err := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); err == nil {
		f.Limit = n
	}
	return f
}

func parseQueryInt64(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
