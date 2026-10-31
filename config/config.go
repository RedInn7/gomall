package conf

import (
	"log"
	"os"
	"strings"

	"github.com/spf13/viper"
)

var Config *Conf

type Conf struct {
	System        *System
	Oss           *Oss
	MySql         map[string]*MySql
	Email         *Email
	Redis         *Redis
	EncryptSecret *EncryptSecret
	RabbitMq      *RabbitMq
	Es            *Es
	PhotoPath     *LocalPhotoPath
}

type RabbitMq struct {
	RabbitMQ         string
	RabbitMQUser     string
	RabbitMQPassWord string
	RabbitMQHost     string
	RabbitMQPort     string
	// RequireOnStartup 为 true 时连不上 broker 直接 fail-fast，
	// 用于生产环境避免静默降级（订单延迟关单 / 事件投递不可用却照常起服务）。
	// 缺省 false 保持开发态容错行为。
	RequireOnStartup bool
	// MaxDeliveryAttempts 单条消息进 DLQ 前允许的最大投递次数，<=0 时取默认值 3。
	MaxDeliveryAttempts int
}

type System struct {
	AppEnv         string
	Domain         string
	Version        string
	HttpPort       string
	Host           string
	UploadModel    string
	AllowedOrigins []string
}

type Oss struct {
	BucketName      string
	AccessKeyId     string
	AccessKeySecret string
	Endpoint        string
	EndpointOut     string
	QiNiuServer     string
}

type MySql struct {
	Dialect  string
	DbHost   string
	DbPort   string
	DbName   string
	UserName string
	Password string
	Charset  string
}

type Email struct {
	ValidEmail string
	SmtpHost   string
	SmtpEmail  string
	SmtpPass   string
}

type Redis struct {
	RedisHost     string
	RedisPort     string
	RedisUsername string
	RedisPassword string
	RedisDbName   int
	RedisNetwork  string
}

// EncryptSecret 加密相关密钥。生产环境请通过环境变量覆盖。
type EncryptSecret struct {
	JwtSecret     string
	SessionSecret string
	EmailSecret   string
	PhoneSecret   string
	MoneySecret   string
}

type LocalPhotoPath struct {
	PhotoHost   string
	ProductPath string
	AvatarPath  string
}

type Es struct {
	EsHost string
	EsPort string
}

func InitConfig() {
	workDir, _ := os.Getwd()
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(workDir + "/config/locales")
	viper.AddConfigPath(workDir)
	err := viper.ReadInConfig()
	if err != nil {
		panic(err)
	}
	err = viper.Unmarshal(&Config)
	if err != nil {
		panic(err)
	}

	applySecretOverrides(Config)
	applySystemOverrides(Config)
	mustHaveSecrets(Config)

	// 参数校验的中文提示；缺文件只影响提示文案，不阻止启动
	if err := LoadLocales(workDir + "/config/locales/zh-cn.yaml"); err != nil {
		log.Printf("加载 zh-cn.yaml 失败，参数校验提示退回字段名: %v", err)
	}
}

func applySystemOverrides(c *Conf) {
	if c == nil || c.System == nil {
		return
	}
	if v := os.Getenv("ALLOWED_ORIGINS"); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		c.System.AllowedOrigins = out
	}
}

func applySecretOverrides(c *Conf) {
	if c == nil || c.EncryptSecret == nil {
		return
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		c.EncryptSecret.JwtSecret = v
	}
	if v := os.Getenv("SESSION_SECRET"); v != "" {
		c.EncryptSecret.SessionSecret = v
	}
	if v := os.Getenv("MONEY_SECRET"); v != "" {
		c.EncryptSecret.MoneySecret = v
	}
}

func mustHaveSecrets(c *Conf) {
	if c == nil || c.EncryptSecret == nil {
		panic("encryptSecret section is required in config")
	}
	if c.EncryptSecret.JwtSecret == "" {
		panic("encryptSecret.jwtSecret is required (set via config or env JWT_SECRET)")
	}
	if c.EncryptSecret.SessionSecret == "" {
		panic("encryptSecret.sessionSecret is required (set via config or env SESSION_SECRET)")
	}
}
