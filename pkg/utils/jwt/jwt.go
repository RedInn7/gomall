package jwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v4"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

func secret() []byte {
	return []byte(conf.Config.EncryptSecret.JwtSecret)
}

type Claims struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	// TokenVersion 撤销机制（版本号方案）：签发时写入用户当前 users.token_version，
	// AuthMiddleware 逐请求比对，不等即拒。改密码 bump 版本号 → 该用户所有已签发
	// token（含被盗的）立即作废。旧 token 无此字段解析为 0，与存量用户 DB 默认值
	// 0 相等 → 存量 token 平滑过渡，首次改密码后才强制重新登录。
	TokenVersion uint `json:"token_version"`
	jwt.RegisteredClaims
}

// GenerateToken 签发用户Token。tokenVersion 传用户当前的 users.token_version。
func GenerateToken(id uint, username string, tokenVersion uint) (accessToken, refreshToken string, err error) {
	nowTime := time.Now()
	expireTime := nowTime.Add(consts.AccessTokenExpireDuration)
	rtExpireTime := nowTime.Add(consts.RefreshTokenExpireDuration)
	claims := Claims{
		ID:           id,
		Username:     username,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expireTime),
			Issuer:    "mall",
		},
	}
	// 加密并获得完整的编码后的字符串token
	accessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret())
	if err != nil {
		return "", "", err
	}

	refreshToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(rtExpireTime),
		Issuer:    "mall",
	}).SignedString(secret())
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, err
}

// ParseToken 验证用户token
func ParseToken(token string) (*Claims, error) {
	tokenClaims, err := jwt.ParseWithClaims(token, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret(), nil
	})
	if tokenClaims != nil {
		if claims, ok := tokenClaims.Claims.(*Claims); ok && tokenClaims.Valid {
			return claims, nil
		}
	}
	return nil, err
}

// parseTokenAllowExpired 解析 token，仅容忍过期错误（ValidationErrorExpired）。
// 签名错误、格式错误等非过期校验失败仍作为错误返回。
func parseTokenAllowExpired(token string) (*Claims, error) {
	tokenClaims, err := jwt.ParseWithClaims(token, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return secret(), nil
	})
	if tokenClaims != nil {
		if claims, ok := tokenClaims.Claims.(*Claims); ok {
			if tokenClaims.Valid {
				return claims, nil
			}
			// token 无效，判断是否仅为过期
			var ve *jwt.ValidationError
			if errors.As(err, &ve) && ve.Errors == jwt.ValidationErrorExpired {
				// 仅过期，claims 仍然可信（签名已通过），允许返回
				return claims, nil
			}
		}
	}
	return nil, err
}

// ParseRefreshToken 验证用户token
func ParseRefreshToken(accessToken, rToken string) (newAToken, newRToken string, err error) {
	// 解析 access token，允许仅过期错误（取出 claims 用于续签）
	// 非过期的验证失败（如签名错误）仍视为致命错误
	accessClaim, err := parseTokenAllowExpired(accessToken)
	if err != nil {
		log.LogrusObj.Infoln("[debug1]err==", err)
		return
	}

	if accessClaim.ExpiresAt != nil && time.Now().Before(accessClaim.ExpiresAt.Time) {
		// access_token 未过期，直接续签两个 token。
		// 版本号原样透传：续签不是重新认证，不能"洗白"版本号——
		// 被 bump 掉的旧 token 续签出的新 token 仍带旧版本号，照样被中间件拒绝。
		return GenerateToken(accessClaim.ID, accessClaim.Username, accessClaim.TokenVersion)
	}

	// access_token 已过期，必须校验 refresh_token（严格校验，包含过期检查）
	refreshClaim, err := ParseToken(rToken)
	if err != nil {
		log.LogrusObj.Infoln("[debug2]err==", err)
		return
	}

	if refreshClaim.ExpiresAt != nil && time.Now().Before(refreshClaim.ExpiresAt.Time) {
		// access_token 过期但 refresh_token 有效，续签（版本号同样原样透传）
		return GenerateToken(accessClaim.ID, accessClaim.Username, accessClaim.TokenVersion)
	}

	// 两者都过期，需要重新登陆
	return "", "", errors.New("身份过期，重新登陆")
}

// EmailClaims 邮件 token 携带的字段。PasswordDigest 必须是 bcrypt 哈希值，绝不能是明文密码。
type EmailClaims struct {
	UserID         uint   `json:"user_id"`
	Email          string `json:"email"`
	PasswordDigest string `json:"password_digest,omitempty"`
	OperationType  uint   `json:"operation_type"`
	jwt.RegisteredClaims
}

// GenerateEmailToken 签发邮箱验证 token。passwordDigest 必须是 bcrypt 哈希值。
func GenerateEmailToken(userID, Operation uint, email, passwordDigest string) (string, error) {
	nowTime := time.Now()
	expireTime := nowTime.Add(15 * time.Minute)
	claims := EmailClaims{
		UserID:         userID,
		Email:          email,
		PasswordDigest: passwordDigest,
		OperationType:  Operation,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expireTime),
			Issuer:    "cmall",
		},
	}
	tokenClaims := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token, err := tokenClaims.SignedString(secret())
	return token, err
}

// ParseEmailToken 验证邮箱验证token
func ParseEmailToken(token string) (*EmailClaims, error) {
	tokenClaims, err := jwt.ParseWithClaims(token, &EmailClaims{}, func(token *jwt.Token) (interface{}, error) {
		return secret(), nil
	})
	if tokenClaims != nil {
		if claims, ok := tokenClaims.Claims.(*EmailClaims); ok && tokenClaims.Valid {
			return claims, nil
		}
	}
	return nil, err
}
