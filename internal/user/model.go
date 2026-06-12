package user

import (
	"errors"
	"strconv"

	"github.com/CocaineCong/secret"
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
)

// ErrMoneyKeyIncorrect 支付密码错误：密文无法用该密钥还原出合法金额
var ErrMoneyKeyIncorrect = errors.New("支付密码错误")

// User 用户模型
type User struct {
	gorm.Model
	UserName       string `gorm:"unique"`
	Email          string
	PasswordDigest string
	NickName       string
	Status         string
	Avatar         string `gorm:"size:1000"`
	Money          string
	Role           string `gorm:"size:16;default:'user';index"`
	Relations      []User `gorm:"many2many:relation;"`
}

const (
	PassWordCost        = 12       // 密码加密难度
	Active       string = "active" // 激活用户
	RoleUser     string = "user"
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

// EncryptMoney 加密金额。底层库在加密失败时直接 panic，这里统一折叠为 error。
func (u *User) EncryptMoney(key string) (money string, err error) {
	defer func() {
		if r := recover(); r != nil {
			money, err = "", errors.New("余额加密失败")
		}
	}()
	aesObj, err := secret.NewAesEncrypt(conf.Config.EncryptSecret.MoneySecret, key, "", secret.AesEncrypt128, secret.AesModeTypeCBC)
	if err != nil {
		return
	}
	money = aesObj.SecretEncrypt(u.Money)

	return
}

// DecryptMoney 解密金额，返回值单位为分。
// 密钥错误时底层 AES-CBC 去填充会越界 panic，这里折叠为支付密码错误；
// 解密成功但明文不是合法整数金额（错误密钥解出乱码）同样按支付密码错误处理，
// 绝不把乱码当余额向上返回。
func (u *User) DecryptMoney(key string) (money int64, err error) {
	defer func() {
		if r := recover(); r != nil {
			money, err = 0, ErrMoneyKeyIncorrect
		}
	}()
	aesObj, err := secret.NewAesEncrypt(conf.Config.EncryptSecret.MoneySecret, key, "", secret.AesEncrypt128, secret.AesModeTypeCBC)
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
