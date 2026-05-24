package e

var MsgFlags = map[int]string{
	SUCCESS:               "ok",
	UpdatePasswordSuccess: "修改密码成功",
	NotExistInentifier:    "该第三方账号未绑定",
	ERROR:                 "fail",
	InvalidParams:         "请求参数错误",

	ErrorExistNick:          "已存在该昵称",
	ErrorExistUser:          "已存在该用户名",
	ErrorNotExistUser:       "该用户不存在",
	ErrorNotCompare:         "账号密码错误",
	ErrorNotComparePassword: "两次密码输入不一致",
	ErrorFailEncryption:     "加密失败",
	ErrorNotExistProduct:    "该商品不存在",
	ErrorNotExistAddress:    "该收获地址不存在",
	ErrorExistFavorite:      "已收藏该商品",
	ErrorUserNotFound:       "用户不存在",

	ErrorBossCheckTokenFail:        "商家的Token鉴权失败",
	ErrorBossCheckTokenTimeout:     "商家Token已超时",
	ErrorBossToken:                 "商家的Token生成失败",
	ErrorBoss:                      "商家Token错误",
	ErrorBossInsufficientAuthority: "商家权限不足",
	ErrorBossProduct:               "商家读文件错误",

	ErrorProductExistCart: "商品已经在购物车了，数量+1",
	ErrorProductMoreCart:  "超过最大上限",

	ErrorAuthCheckTokenFail:        "Token鉴权失败",
	ErrorAuthCheckTokenTimeout:     "Token已超时",
	ErrorAuthToken:                 "Token生成失败",
	ErrorAuth:                      "Token错误",
	ErrorAuthInsufficientAuthority: "权限不足",
	ErrorReadFile:                  "读文件失败",
	ErrorSendEmail:                 "发送邮件失败",
	ErrorCallApi:                   "调用接口失败",
	ErrorUnmarshalJson:             "解码JSON失败",

	ErrorUploadFile:    "上传失败",
	ErrorAdminFindUser: "管理员查询用户失败",

	ErrorDatabase: "数据库操作出错,请重试",

	ErrorOss: "OSS配置错误",

	ErrIdempotencyTokenInvalid: "幂等token不存在或已过期",
	ErrIdempotencyInProgress:   "请求正在处理中，请勿重复提交",

	ErrRateLimitExceeded: "请求频率超限，请稍后重试",
	ErrCircuitOpen:       "下游服务熔断中，暂时不可用",

	PromoRuleExpired:       "满减活动已结束或未开始",
	PromoRuleNotApplicable: "当前购物车未满足满减门槛或不在适用范围",
	PromoBudgetExhausted:   "本场满减活动当日预算已用完，欢迎下次再来",

	ErrGroupbuyFull:          "该团已满员，请发起新团或加入其他团",
	ErrGroupbuyExpired:       "该团已超时未成团，款项将于 1-3 工作日原路退回",
	ErrGroupbuyDuplicateJoin: "您已加入过该团，不能重复参团",
	ErrGroupbuyClosed:        "该团已关闭",

	ErrPreorderNotInDepositWindow: "当前不在预售定金期，无法支付定金",
	ErrPreorderNotInFinalWindow:   "当前不在尾款支付窗口，无法支付尾款",
	ErrPreorderDepositNotPaid:     "尚未支付定金，无法支付尾款",
	ErrPreorderForfeitedDeposit:   "预售已结束，根据预售须知定金不予退还",
}

// GetMsg 获取状态码对应信息
func GetMsg(code int) string {
	msg, ok := MsgFlags[code]
	if ok {
		return msg
	}
	return MsgFlags[ERROR]
}
