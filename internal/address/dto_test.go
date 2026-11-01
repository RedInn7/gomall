package address

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin/binding"
)

func TestAddressReqValidation(t *testing.T) {
	ok := AddressCreateReq{Name: "张三", Phone: "13800000000", Address: "上海市徐汇区漕溪北路1号"}
	if err := binding.Validator.ValidateStruct(&ok); err != nil {
		t.Fatalf("valid address rejected: %v", err)
	}
	bad := []AddressCreateReq{
		{Phone: "13800000000", Address: "a"},                                 // 缺姓名
		{Name: "张三", Phone: "1380000000", Address: "a"},                      // 10 位
		{Name: "张三", Phone: "1380000000x", Address: "a"},                     // 非数字
		{Name: "张三", Phone: "+1380000000", Address: "a"},                     // 带符号
		{Name: "张三", Phone: "123456789.0", Address: "a"},                     // 带小数点
		{Name: strings.Repeat("张", 21), Phone: "13800000000", Address: "a"},  // 超列宽
		{Name: "张三", Phone: "13800000000", Address: strings.Repeat("路", 51)}, // 超列宽
		{Name: "张三", Phone: "13800000000"},                                   // 缺地址
	}
	for i, req := range bad {
		if err := binding.Validator.ValidateStruct(&req); err == nil {
			t.Fatalf("case %d should be rejected: %+v", i, req)
		}
	}
	// 中文按字符计：20 个汉字刚好放得下 varchar(20)
	edge := AddressServiceReq{Id: 1, Name: strings.Repeat("张", 20), Phone: "13800000000", Address: "a"}
	if err := binding.Validator.ValidateStruct(&edge); err != nil {
		t.Fatalf("20-char name rejected: %v", err)
	}
	if err := binding.Validator.ValidateStruct(&AddressDeleteReq{}); err == nil {
		t.Fatal("delete without id should be rejected")
	}
}
