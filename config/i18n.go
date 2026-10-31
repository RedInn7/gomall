package conf

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Dictinary 参数校验提示的字典，由 InitConfig 从 config/locales/zh-cn.yaml 加载
var Dictinary map[string]interface{}

// LoadLocales 读取国际化文件
func LoadLocales(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m := make(map[string]interface{})
	if err := yaml.Unmarshal(data, &m); err != nil {
		return err
	}
	Dictinary = m
	return nil
}

// T 按点分路径翻译，如 T("Field.UserName") 返回 "用户名"。
// 字典没加载或查不到时返回 key 的最后一段（"Field.Email" 返回 "Email"），不会 panic。
func T(key string) string {
	keys := strings.Split(key, ".")
	var cur interface{} = Dictinary
	for _, k := range keys {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return keys[len(keys)-1]
		}
		cur = m[k]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return keys[len(keys)-1]
}
