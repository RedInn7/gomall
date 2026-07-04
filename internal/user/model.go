package user

import (
	"errors"
	"strconv"

	"github.com/CocaineCong/secret"
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
	"golang.org/x/crypto/bcrypt"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
)

// ErrMoneyKeyIncorrect 支付密码错误：密文无法用该密钥还原出合法金额
var ErrMoneyKeyIncorrect = errors.New("支付密码错误")

// User 用户模型
type User struct {
	dbmodel.Model
	UserName       string `gorm:"unique"`
	Email          string
	PasswordDigest string
	NickName       string
	Status         string
	Avatar         string `gorm:"size:1000"`
	Money          string
	// MoneyPasswordDigest 6 位支付密码的 bcrypt 摘要。余额改用服务端密钥加密后，
	// 支付密码不再参与加解密，单独存摘要用于支付前的身份校验。
	MoneyPasswordDigest string
	Role                string `gorm:"size:16;default:'user';index"`
	// TokenVersion JWT 撤销版本号：签 token 时写入 claims，AuthMiddleware 逐请求比对。
	// 改密码/强制下线时 +1（BumpTokenVersion），该用户所有已签发 token 立即作废。
	// 默认 0 与不带该字段的存量 token（解析为 0）相等，存量会话平滑过渡。
	TokenVersion uint   `gorm:"not null;default:0"`
	Relations    []User `gorm:"many2many:relation;"`
}

const (
	PassWordCost        = 12       // 密码加密难度
	Active       string = "active" // 激活用户
	RoleUser     string = "user"
	RoleMerchant string = "merchant"
	RoleAdmin    string = "admin"
)

// SetPassword 设置密码
func (u *User) SetPassword(password string) error {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), PassWordCost)
	if err != nil {
		return err
	}
	u.PasswordDigest = string(bytes)
	return nil
}

// CheckPassword 校验密码
func (u *User) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.PasswordDigest), []byte(password))
	return err == nil
}

// AvatarURL 头像地址
func (u *User) AvatarURL() string {
	if conf.Config.System.UploadModel == consts.UploadModelOss {
		return u.Avatar
	}
	pConfig := conf.Config.PhotoPath
	return pConfig.PhotoHost + conf.Config.System.HttpPort + pConfig.AvatarPath + u.Avatar
}

// moneyAesKey 余额密文落库使用的服务端固定密钥位。真正的机密是 specialSign
// (conf.Config.EncryptSecret.MoneySecret，来自 env MONEY_SECRET)，这里只需提供一个
// 非空的 key 槽位（底层库不接受空 key）。绝不能用用户支付密码，否则商家余额会被
// 买家密码解密，导致跨账户串密钥的资金错乱。
const moneyAesKey = "gomall_money"

// EncryptMoney 用服务端密钥加密余额。底层库在加密失败时直接 panic，这里统一折叠为 error。
func (u *User) EncryptMoney() (money string, err error) {
	defer func() {
		if r := recover(); r != nil {
			money, err = "", errors.New("余额加密失败")
		}
	}()
	aesObj, err := secret.NewAesEncrypt(conf.Config.EncryptSecret.MoneySecret, moneyAesKey, "", secret.AesEncrypt128, secret.AesModeTypeCBC)
	if err != nil {
		return
	}
	money = aesObj.SecretEncrypt(u.Money)

	return
}

// DecryptMoney 用服务端密钥解密余额，返回值单位为分。
// 解出的明文若不是合法整数金额（账本损坏 / 密钥轮换不一致）折叠为 ErrMoneyKeyIncorrect，
// 绝不把乱码当余额向上返回。
func (u *User) DecryptMoney() (money int64, err error) {
	defer func() {
		if r := recover(); r != nil {
			money, err = 0, ErrMoneyKeyIncorrect
		}
	}()
	aesObj, err := secret.NewAesEncrypt(conf.Config.EncryptSecret.MoneySecret, moneyAesKey, "", secret.AesEncrypt128, secret.AesModeTypeCBC)
	if err != nil {
		return
	}

	plain := aesObj.SecretDecrypt(u.Money)
	money, err = strconv.ParseInt(plain, 10, 64)
	if err != nil {
		return 0, ErrMoneyKeyIncorrect
	}
	return money, nil
}

// SetMoneyPassword 设置 6 位支付密码摘要（bcrypt）。
func (u *User) SetMoneyPassword(pin string) error {
	bytes, err := bcrypt.GenerateFromPassword([]byte(pin), PassWordCost)
	if err != nil {
		return err
	}
	u.MoneyPasswordDigest = string(bytes)
	return nil
}

// CheckMoneyPassword 校验支付密码。摘要为空（历史 / 种子用户未设密码）时放行，
// 否则做 bcrypt 比对。
func (u *User) CheckMoneyPassword(pin string) bool {
	if u.MoneyPasswordDigest == "" {
		return true
	}
	return bcrypt.CompareHashAndPassword([]byte(u.MoneyPasswordDigest), []byte(pin)) == nil
}
