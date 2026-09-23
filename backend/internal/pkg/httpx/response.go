// Package httpx 统一 API 响应结构，与前端 axios 拦截器约定保持一致。
package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Body struct {
	Code    int    `json:"code"` // 0 = 成功，非 0 = 业务错误码
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{Code: 0, Message: "ok", Data: data})
}

func Fail(c *gin.Context, httpStatus, code int, message string) {
	c.JSON(httpStatus, Body{Code: code, Message: message})
}

func FailBadRequest(c *gin.Context, message string) {
	Fail(c, http.StatusBadRequest, 400, message)
}

func FailUnauthorized(c *gin.Context, message string) {
	Fail(c, http.StatusUnauthorized, 401, message)
}

func FailNotFound(c *gin.Context, message string) {
	Fail(c, http.StatusNotFound, 404, message)
}

// FailUpstream 依赖外部系统（如 SSH 到目标服务器）失败：客户端可重试。
func FailUpstream(c *gin.Context, message string) {
	Fail(c, http.StatusBadGateway, 502, message)
}

func FailServer(c *gin.Context, err error) {
	Fail(c, http.StatusInternalServerError, 500, err.Error())
}
