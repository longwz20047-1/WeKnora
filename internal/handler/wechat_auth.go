package handler

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WeChatAuthHandler implements HTTP request handlers for WeChat Work OAuth authentication
type WeChatAuthHandler struct {
	wechatService interfaces.WeChatAuthService
	oauthRepo     interfaces.OAuthBindingRepository
	userService   interfaces.UserService
	userRepo      interfaces.UserRepository
}

// NewWeChatAuthHandler creates a new WeChat auth handler instance
func NewWeChatAuthHandler(
	wechatService interfaces.WeChatAuthService,
	oauthRepo interfaces.OAuthBindingRepository,
	userService interfaces.UserService,
	userRepo interfaces.UserRepository,
) *WeChatAuthHandler {
	return &WeChatAuthHandler{
		wechatService: wechatService,
		oauthRepo:     oauthRepo,
		userService:   userService,
		userRepo:      userRepo,
	}
}

// setPasswordRequest is the request body for SetPassword endpoint
type setPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=6,max=72"`
}

// GetConfig godoc
// @Summary      获取企业微信登录配置
// @Description  返回企业微信 OAuth 是否启用及 CorpID
// @Tags         企业微信认证
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  errors.AppError
// @Router       /auth/wechat/config [get]
func (h *WeChatAuthHandler) GetConfig(c *gin.Context) {
	ctx := c.Request.Context()

	config, err := h.wechatService.GetConfig(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get WeChat config: %v", err)
		appErr := errors.NewInternalServerError("Failed to get WeChat config").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":  config.IsEnabled,
			"corp_id":  config.CorpID,
			"agent_id": config.AgentID,
		},
	})
}

// GenerateQRCode godoc
// @Summary      生成企业微信扫码登录二维码
// @Description  生成用于扫码登录的 ticket 和二维码 URL
// @Tags         企业微信认证
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  errors.AppError
// @Router       /auth/wechat/qrcode [post]
func (h *WeChatAuthHandler) GenerateQRCode(c *gin.Context) {
	ctx := c.Request.Context()

	ticket, qrcodeURL, err := h.wechatService.GenerateQRCode(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to generate QR code: %v", err)
		appErr := errors.NewInternalServerError("Failed to generate QR code").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"ticket":     ticket,
			"qrcode_url": qrcodeURL,
		},
	})
}

// PollStatus godoc
// @Summary      轮询扫码状态
// @Description  根据 ticket 查询扫码登录状态
// @Tags         企业微信认证
// @Produce      json
// @Param        ticket  path  string  true  "扫码 ticket"
// @Success      200     {object}  map[string]interface{}
// @Failure      400     {object}  errors.AppError
// @Failure      500     {object}  errors.AppError
// @Router       /auth/wechat/poll/{ticket} [get]
func (h *WeChatAuthHandler) PollStatus(c *gin.Context) {
	ctx := c.Request.Context()

	ticket := c.Param("ticket")
	if ticket == "" {
		appErr := errors.NewValidationError("Ticket is required")
		c.Error(appErr)
		return
	}

	status, loginResp, err := h.wechatService.PollTicketStatus(ctx, ticket)
	if err != nil {
		logger.Errorf(ctx, "Failed to poll ticket status: %v", err)
		appErr := errors.NewInternalServerError("Failed to poll ticket status").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"status":         status,
			"login_response": loginResp,
		},
	})
}

// OAuthCallback godoc
// @Summary      企业微信 OAuth 回调
// @Description  处理企业微信 OAuth 授权回调
// @Tags         企业微信认证
// @Produce      json
// @Param        code   query  string  true  "授权码"
// @Param        state  query  string  true  "状态参数"
// @Success      200    {object}  map[string]interface{}
// @Failure      400    {object}  errors.AppError
// @Failure      500    {object}  errors.AppError
// @Router       /auth/wechat/callback [get]
func (h *WeChatAuthHandler) OAuthCallback(c *gin.Context) {
	ctx := c.Request.Context()

	code := c.Query("code")
	state := c.Query("state")

	if code == "" {
		appErr := errors.NewValidationError("OAuth code is required")
		c.Error(appErr)
		return
	}
	if state == "" {
		appErr := errors.NewValidationError("OAuth state is required")
		c.Error(appErr)
		return
	}

	_, err := h.wechatService.HandleCallback(ctx, code, state)
	if err != nil {
		logger.Errorf(ctx, "Failed to handle OAuth callback: %v", err)
		appErr := errors.NewInternalServerError("OAuth callback failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Redirect browser back to the frontend login page with ticket.
	// Use qrcode_redirect_url from config as the frontend base URL.
	config, _ := h.wechatService.GetConfig(ctx)
	frontendBase := "/login"
	if config != nil && config.QRCodeRedirectURL != "" {
		frontendBase = config.QRCodeRedirectURL
	}

	redirectURL := frontendBase + "?wechat_ticket=" + url.QueryEscape(state)
	c.Redirect(http.StatusFound, redirectURL)
}

// GetBinding godoc
// @Summary      获取当前用户的企业微信绑定信息
// @Description  查询当前登录用户的 OAuth 绑定状态
// @Tags         企业微信认证
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Router       /auth/wechat/binding [get]
func (h *WeChatAuthHandler) GetBinding(c *gin.Context) {
	ctx := c.Request.Context()

	user, exists := c.Get(types.UserContextKey.String())
	if !exists {
		appErr := errors.NewUnauthorizedError("User not found in context")
		c.Error(appErr)
		return
	}
	currentUser := user.(*types.User)

	binding, err := h.oauthRepo.GetByUserID(ctx, currentUser.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get OAuth binding for user %s: %v", currentUser.ID, err)
		appErr := errors.NewInternalServerError("Failed to get binding information").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    binding,
	})
}

// Unbind godoc
// @Summary      解绑企业微信
// @Description  解除当前用户的企业微信 OAuth 绑定
// @Tags         企业微信认证
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  errors.AppError
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Router       /auth/wechat/unbind [delete]
func (h *WeChatAuthHandler) Unbind(c *gin.Context) {
	ctx := c.Request.Context()

	user, exists := c.Get(types.UserContextKey.String())
	if !exists {
		appErr := errors.NewUnauthorizedError("User not found in context")
		c.Error(appErr)
		return
	}
	currentUser := user.(*types.User)

	if err := h.oauthRepo.DeleteByUserID(ctx, currentUser.ID); err != nil {
		logger.Errorf(ctx, "Failed to unbind WeChat for user %s: %v", currentUser.ID, err)
		appErr := errors.NewInternalServerError("Failed to unbind WeChat account").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Infof(ctx, "WeChat unbound successfully for user %s", currentUser.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Unbound successfully",
	})
}

// SetPassword godoc
// @Summary      设置密码（企业微信用户首次设置）
// @Description  企业微信 OAuth 用户首次设置本地密码，仅限已绑定用户
// @Tags         企业微信认证
// @Accept       json
// @Produce      json
// @Param        request  body  setPasswordRequest  true  "新密码"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  errors.AppError
// @Failure      401      {object}  errors.AppError
// @Failure      403      {object}  errors.AppError
// @Failure      500      {object}  errors.AppError
// @Security     Bearer
// @Router       /auth/wechat/set-password [post]
func (h *WeChatAuthHandler) SetPassword(c *gin.Context) {
	ctx := c.Request.Context()

	var req setPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		appErr := errors.NewValidationError("Invalid request parameters").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	user, exists := c.Get(types.UserContextKey.String())
	if !exists {
		appErr := errors.NewUnauthorizedError("User not found in context")
		c.Error(appErr)
		return
	}
	currentUser := user.(*types.User)

	// Only users with WeChat OAuth binding can set password this way
	hasBind, err := h.oauthRepo.HasBinding(ctx, currentUser.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to check binding for user %s: %v", currentUser.ID, err)
		appErr := errors.NewInternalServerError("Failed to verify binding status").WithDetails(err.Error())
		c.Error(appErr)
		return
	}
	if !hasBind {
		appErr := errors.NewForbiddenError("Only WeChat OAuth users can set password without old password")
		c.Error(appErr)
		return
	}

	// Get full user record from repository
	fullUser, err := h.userRepo.GetUserByID(ctx, currentUser.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get user %s: %v", currentUser.ID, err)
		appErr := errors.NewInternalServerError("Failed to get user information").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Hash the new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		logger.Errorf(ctx, "Failed to hash password: %v", err)
		appErr := errors.NewInternalServerError("Failed to set password")
		c.Error(appErr)
		return
	}

	fullUser.PasswordHash = string(hashedPassword)
	if err := h.userRepo.UpdateUser(ctx, fullUser); err != nil {
		logger.Errorf(ctx, "Failed to update password for user %s: %v", currentUser.ID, err)
		appErr := errors.NewInternalServerError("Failed to save password").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Infof(ctx, "Password set successfully for WeChat user %s", currentUser.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Password set successfully",
	})
}
