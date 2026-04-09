package wechat

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/xen0n/go-workwx"
)

// Client 企业微信 SDK 客户端封装
type Client struct {
	app *workwx.WorkwxApp
}

// NewClient 创建企业微信客户端
// SDK 内部自动管理应用级 access_token 的获取和刷新
func NewClient(corpID string, corpSecret string, agentID int64) *Client {
	corp := workwx.New(corpID)
	app := corp.WithApp(corpSecret, agentID)
	return &Client{app: app}
}

// GetUserIdentityByCode 通过 OAuth code 获取用户身份（userid）
// OAuth 流程: 用户扫码/授权 → 回调带 code → 此方法获取 userid
func (c *Client) GetUserIdentityByCode(ctx context.Context, code string) (string, error) {
	identity, err := c.app.GetUserInfoByCode(code)
	if err != nil {
		return "", fmt.Errorf("get user identity by code: %w", err)
	}
	return identity.UserID, nil
}

// GetUserDetail 获取成员详细信息（需要通讯录读取权限）
func (c *Client) GetUserDetail(ctx context.Context, userID string) (*types.WeChatUserInfo, error) {
	userInfo, err := c.app.GetUser(userID)
	if err != nil {
		return nil, fmt.Errorf("get user detail: %w", err)
	}

	// 从 SDK UserInfo 的 Departments ([]UserDeptInfo) 提取部门 ID 列表
	deptIDs := make([]int, len(userInfo.Departments))
	for i, dept := range userInfo.Departments {
		deptIDs[i] = int(dept.DeptID)
	}

	// 将 SDK 的 UserGender(int) 转为字符串
	gender := ""
	switch userInfo.Gender {
	case workwx.UserGenderMale:
		gender = "1"
	case workwx.UserGenderFemale:
		gender = "2"
	default:
		gender = "0"
	}

	// SDK 的 IsEnabled 是 bool, Status 是 UserStatus(int)
	enableVal := 0
	if userInfo.IsEnabled {
		enableVal = 1
	}

	return &types.WeChatUserInfo{
		UserID:     userInfo.UserID,
		Name:       userInfo.Name,
		Email:      userInfo.Email,
		Mobile:     userInfo.Mobile,
		Avatar:     userInfo.AvatarURL,
		Department: deptIDs,
		Position:   userInfo.Position,
		Gender:     gender,
		Status:     int(userInfo.Status),
		Enable:     enableVal,
	}, nil
}
