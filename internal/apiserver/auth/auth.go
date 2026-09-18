// Package auth 提供 GPT-GO 控制台的全局登录门禁。
//
// 设计:整个控制台共用一道密码(默认 admin/1024,可在系统设置修改),
// 登录成功发一个全局会话 cookie(auth.CookieName);除登录/健康检查外,
// 所有 /api/* 都必须携带该 cookie,否则 401。iCloud 等子模块复用同一会话,
// 不再单独要求登录。会话与密码哈希由 icloud 模块的 auth.Service 托管(Mongo 存储)。
package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	icloudauth "gpt-go/internal/icloud/auth"
)

// 全局会话 cookie 名与属性(与 icloudauth.CookieName 一致,整套控制台共用)。
const cookieName = icloudauth.CookieName

// Guard 返回 gin 中间件:校验全局会话 cookie,未登录则 401。
//
// excludePrefixes 列出的路径前缀跳过校验(如 /api/auth/login、/api/health)。
// iCloud 公开 API(/api/icloud/v1/)用 API key 鉴权,也不走此 cookie,需排除。
func Guard(svc *icloudauth.Service, excludePrefixes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		// 只拦截 API 请求;前端静态页面(HTML/JS/CSS)放行,由 React 的
		// ConsoleGate 在未登录时自行渲染登录页(否则登录页本身都进不来)。
		if !strings.HasPrefix(path, "/api/") {
			c.Next()
			return
		}
		for _, p := range excludePrefixes {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}
		cookie, err := c.Request.Cookie(cookieName)
		if err != nil || cookie.Value == "" {
			abortUnauthorized(c)
			return
		}
		if _, ok := svc.Authenticate(cookie.Value); !ok {
			abortUnauthorized(c)
			return
		}
		c.Next()
	}
}

func abortUnauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"detail": gin.H{"code": "auth_required", "message": "请先登录控制台"},
	})
}

// Handler 提供全局登录/登出/状态/改密码接口。
type Handler struct {
	svc *icloudauth.Service
}

// NewHandler 构造全局鉴权 Handler。
func NewHandler(svc *icloudauth.Service) *Handler {
	return &Handler{svc: svc}
}

// Register 挂载全局鉴权路由(这些路由自身不校验 cookie)。
func (h *Handler) Register(rg *gin.RouterGroup) {
	rg.GET("/status", h.status)
	rg.POST("/login", h.login)
	rg.POST("/logout", h.logout)
	rg.POST("/password", h.changePassword)
}

type loginBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type passwordBody struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) status(c *gin.Context) {
	authenticated := false
	if cookie, err := c.Request.Cookie(cookieName); err == nil {
		if _, ok := h.svc.Authenticate(cookie.Value); ok {
			authenticated = true
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"setup_required": !h.svc.HasAdmin(),
		"authenticated":  authenticated,
	})
}

func (h *Handler) login(c *gin.Context) {
	var body loginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeErr(c, http.StatusBadRequest, "invalid_body", "请求格式错误")
		return
	}
	result, err := h.svc.Login(body.Username, body.Password)
	if err != nil {
		writeErr(c, http.StatusUnauthorized, "login_failed", err.Error())
		return
	}
	setSessionCookie(c, result.Token, result.ExpiresAt)
	c.JSON(http.StatusOK, gin.H{"admin": gin.H{"username": result.Admin.Username, "last_login_at": result.Admin.LastLoginAt}})
}

func (h *Handler) logout(c *gin.Context) {
	if cookie, err := c.Request.Cookie(cookieName); err == nil {
		_ = h.svc.Logout(cookie.Value)
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) changePassword(c *gin.Context) {
	// 改密码必须先登录。
	cookie, err := c.Request.Cookie(cookieName)
	if err != nil {
		abortUnauthorized(c)
		return
	}
	if _, ok := h.svc.Authenticate(cookie.Value); !ok {
		abortUnauthorized(c)
		return
	}
	var body passwordBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeErr(c, http.StatusBadRequest, "invalid_body", "请求格式错误")
		return
	}
	if err := h.svc.ChangePassword(body.OldPassword, body.NewPassword); err != nil {
		writeErr(c, http.StatusBadRequest, "change_failed", err.Error())
		return
	}
	// 旧会话已全部失效,清除当前 cookie 让前端回登录页。
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func writeErr(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"detail": gin.H{"code": code, "message": message}})
}

func setSessionCookie(c *gin.Context, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	secure := c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", Expires: expiresAt, MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}
