package jwt

import (
	"errors"
	"time"

	"github.com/dgrijalva/jwt-go"

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
	jwt.StandardClaims
}

// GenerateToken 签发用户Token
func GenerateToken(id uint, username string) (accessToken, refreshToken string, err error) {
	nowTime := time.Now()
	expireTime := nowTime.Add(consts.AccessTokenExpireDuration)
	rtExpireTime := nowTime.Add(consts.RefreshTokenExpireDuration)
	claims := Claims{
		ID:       id,
		Username: username,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: expireTime.Unix(),
			Issuer:    "mall",
		},
	}
	// 加密并获得完整的编码后的字符串token
	accessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret())
	if err != nil {
		return "", "", err
	}

	refreshToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.StandardClaims{
		ExpiresAt: rtExpireTime.Unix(),
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

// ParseRefreshToken 验证用户token
func ParseRefreshToken(accessToken, rToken string) (newAToken, newRToken string, err error) {
	accessClaim, err := ParseToken(accessToken)
	if err != nil {
		log.LogrusObj.Infoln("[debug1]err==", err)
		return
	}

	refreshClaim, err := ParseToken(rToken)
	if err != nil {
		log.LogrusObj.Infoln("[debug2]err==", err)
		return
	}

	if accessClaim.ExpiresAt > time.Now().Unix() {
		// 如果 access_token 没过期,每一次请求都刷新 refresh_token 和 access_token
		return GenerateToken(accessClaim.ID, accessClaim.Username)
	}

	if refreshClaim.ExpiresAt > time.Now().Unix() {
		// 如果 access_token 过期了,但是 refresh_token 没过期, 刷新 refresh_token 和 access_token
		return GenerateToken(accessClaim.ID, accessClaim.Username)
	}

	// 如果两者都过期了,重新登陆
	return "", "", errors.New("身份过期，重新登陆")
}

// EmailClaims 邮件 token 携带的字段。PasswordDigest 必须是 bcrypt 哈希值，绝不能是明文密码。
type EmailClaims struct {
	UserID         uint   `json:"user_id"`
	Email          string `json:"email"`
	PasswordDigest string `json:"password_digest,omitempty"`
	OperationType  uint   `json:"operation_type"`
	jwt.StandardClaims
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
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: expireTime.Unix(),
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
