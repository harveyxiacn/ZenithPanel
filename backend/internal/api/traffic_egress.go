package api

import (
	"encoding/csv"
	"fmt"
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

	rg.GET("/traffic/egress/export", func(c *gin.Context) {
		if !guard(c) {
			return
		}
		scope := strings.TrimSpace(c.DefaultQuery("scope", "detail"))
		f := egressFilterFromQuery(c)
		filename := fmt.Sprintf("zenith-egress-%s-%s.csv", scope, time.Now().UTC().Format("20060102-150405"))
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		c.Writer.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM so Excel renders CJK
		w := csv.NewWriter(c.Writer)
		n := 0
		if scope == "dest" {
			w.Write([]string{"key", "kind", "as_org", "country", "bytes_up", "bytes_down", "bytes_total", "hits"})
			for _, r := range eg.Summary(f, "dest") {
				w.Write([]string{r.Key, r.Kind, r.ASOrg, r.Country, strconv.FormatInt(r.BytesUp, 10), strconv.FormatInt(r.BytesDown, 10), strconv.FormatInt(r.BytesTotal, 10), strconv.FormatInt(r.Hits, 10)})
				n++
			}
		} else {
			w.Write([]string{"time", "bucket", "instance", "user_email", "dest_host", "dest_ip", "dest_rdns", "asn", "as_org", "country", "direction", "bytes_up", "bytes_down", "hits"})
			for _, r := range eg.Export(f) {
				w.Write([]string{time.Unix(r.Bucket, 0).UTC().Format(time.RFC3339), strconv.FormatInt(r.Bucket, 10), r.Instance, r.UserEmail, r.DestHost, r.DestIP, r.DestRDNS, r.ASN, r.ASOrg, r.Country, r.Direction, strconv.FormatInt(r.BytesUp, 10), strconv.FormatInt(r.BytesDown, 10), strconv.FormatInt(r.Hits, 10)})
				n++
			}
		}
		w.Flush()
		recordAudit(c, "traffic.egress.export", fmt.Sprintf("scope=%s rows=%d", scope, n))
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
