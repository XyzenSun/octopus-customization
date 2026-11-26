package handlers

import (
	"encoding/json"
    "errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/privacy"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/setting").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getSettingList),
		).
		AddRoute(
			router.NewRoute("/set", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(setSetting),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Handle(exportDB),
		).
		AddRoute(
			router.NewRoute("/import", http.MethodPost).
				Handle(importDB),
		)
}

func getSettingList(c *gin.Context) {
	settings, err := op.SettingList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, settings)
}

func setSetting(c *gin.Context) {
    var input struct {
        Key   model.SettingKey `json:"key"`
        Value json.RawMessage  `json:"value"`
    }
    if err := c.ShouldBindJSON(&input); err != nil {
        // 解析错误不得回显无效隐私值或其类型.
        resp.Error(c, http.StatusBadRequest, "invalid setting input")
        return
    }
    setting := model.Setting{Key: input.Key}
    if model.IsPrivacySettingKey(setting.Key) {
        value, err := privacy.DecodeJSONString(input.Value)
        if err != nil {
            resp.Error(c, http.StatusBadRequest, privacy.ErrInvalidConfig.Error())
            return
        }
        setting.Value = value
    } else if len(input.Value) != 0 {
        if err := json.Unmarshal(input.Value, &setting.Value); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
        }
	}
	// 两步验证开关必须走 /user/2fa/enable|disable，校验 TOTP 验证码。

	if setting.Key == model.SettingKeyTwoFactorEnabled {
		resp.Error(c, http.StatusForbidden, "two factor authentication must be changed via /api/v1/user/2fa")
		return
	}
	if err := setting.Validate(); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
    var err error
    if model.IsPrivacySettingKey(setting.Key) {
        err = op.PrivacySetSetting(c.Request.Context(), setting.Key, setting.Value)
    } else {
        err = op.SettingSetString(setting.Key, setting.Value)
    }
    if err != nil {
        status := http.StatusInternalServerError
        if errors.Is(err, privacy.ErrInvalidConfig) {
            status = http.StatusBadRequest
        }
        resp.Error(c, status, err.Error())
		return
	}
	switch setting.Key {
	case model.SettingKeyModelInfoUpdateInterval:
		hours, err := strconv.Atoi(setting.Value)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		task.Update(string(setting.Key), time.Duration(hours)*time.Hour)
	case model.SettingKeySyncLLMInterval:
		hours, err := strconv.Atoi(setting.Value)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		task.Update(string(setting.Key), time.Duration(hours)*time.Hour)
	}
	resp.Success(c, setting)
}

func exportDB(c *gin.Context) {
	includeLogs, _ := strconv.ParseBool(c.DefaultQuery("include_logs", "false"))
	includeStats, _ := strconv.ParseBool(c.DefaultQuery("include_stats", "false"))

	dump, err := op.DBExportAll(c.Request.Context(), includeLogs, includeStats)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=\"octopus-export-"+time.Now().Format("20060102150405")+".json\"")
	c.JSON(http.StatusOK, dump)
}

func importDB(c *gin.Context) {
	var dump model.DBDump

	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		fh, err := c.FormFile("file")
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "missing upload file field 'file'")
			return
		}
		f, err := fh.Open()
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := decodeDBDump(body, &dump); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := decodeDBDump(body, &dump); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	result, err := op.DBImportIncremental(c.Request.Context(), &dump)
	if err != nil {
        status := http.StatusBadRequest
        switch {
        case errors.Is(err, privacy.ErrRuleConflict):
            status = http.StatusConflict
        case errors.Is(err, op.ErrImportCommittedCacheRefresh), errors.Is(err, op.ErrBackupStorage),
            errors.Is(err, op.ErrPrivacyStorage), errors.Is(err, privacy.ErrCompile):
            status = http.StatusInternalServerError
        }
        resp.Error(c, status, err.Error())
		return
	}

	resp.Success(c, result)
}

func decodeDBDump(body []byte, dump *model.DBDump) error {
	if dump == nil {
		return json.Unmarshal(body, &struct{}{})
	}

	if err := json.Unmarshal(body, dump); err != nil {
		return err
	}

	if dump.Version == 0 &&
		len(dump.Channels) == 0 &&
        len(dump.ChannelKeys) == 0 &&
        len(dump.PrivacyRules) == 0 &&
		len(dump.Groups) == 0 &&
		len(dump.GroupItems) == 0 &&
		len(dump.Settings) == 0 &&
		len(dump.APIKeys) == 0 &&
		len(dump.LLMInfos) == 0 &&
		len(dump.RelayLogs) == 0 &&
		len(dump.StatsDaily) == 0 &&
		len(dump.StatsHourly) == 0 &&
		len(dump.StatsTotal) == 0 &&
		len(dump.StatsChannel) == 0 &&
		len(dump.StatsModel) == 0 &&
		len(dump.StatsAPIKey) == 0 {
		var wrapper struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &wrapper); err == nil && len(wrapper.Data) > 0 {
			return json.Unmarshal(wrapper.Data, dump)
		}
	}

	return nil
}
