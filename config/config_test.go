package conf

import (
	"testing"

	"github.com/spf13/viper"
)

// 示例配置里的每个键都必须被代码读取，避免再出现 email.address 这种写了也不生效的键
func TestExampleConfigMatchesStruct(t *testing.T) {
	v := viper.New()
	v.SetConfigFile("locales/config.example.yaml")
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	var c Conf
	if err := v.UnmarshalExact(&c); err != nil {
		t.Fatalf("config.example.yaml 有代码不读取的键: %v", err)
	}
	if c.Email == nil || c.Email.ValidEmail == "" {
		t.Fatal("email.validEmail 没有读到")
	}
}
