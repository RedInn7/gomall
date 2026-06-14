package user

import (
	"fmt"
	"testing"

	conf "github.com/RedInn7/gomall/config"
)

func TestMain(m *testing.M) {
	re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
	conf.InitConfigForTest(&re)
	fmt.Println("Write tests on values: ", conf.Config)
	m.Run()
}

func TestUserModelEncryptMoney(t *testing.T) {
	u := User{
		UserName: "FanOne",
		Money:    "10000",
	}
	t.Logf("u before encrypt money:%s", u.Money)
	money, err := u.EncryptMoney()
	u.Money = money
	if err != nil {
		fmt.Println("err EncryptMoney", err)
	}
	t.Logf("u after encrypt money:%s", u.Money)
	m, err := u.DecryptMoney()
	if err != nil {
		fmt.Println("err EncryptMoney", err)
	}
	t.Logf("u after decrypt money(分):%d", m)
}

// TestUserModelMoneyPassword 支付密码摘要：空摘要放行（兼容种子用户），
// 设过密码后只接受正确 PIN。
func TestUserModelMoneyPassword(t *testing.T) {
	u := User{UserName: "FanOne"}
	if !u.CheckMoneyPassword("123456") {
		t.Fatal("空摘要应放行")
	}
	if err := u.SetMoneyPassword("123456"); err != nil {
		t.Fatalf("SetMoneyPassword: %v", err)
	}
	if !u.CheckMoneyPassword("123456") {
		t.Fatal("正确支付密码应通过")
	}
	if u.CheckMoneyPassword("654321") {
		t.Fatal("错误支付密码应被拒")
	}
}
