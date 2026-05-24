package e

const (
	SUCCESS               = 200
	UpdatePasswordSuccess = 201
	NotExistInentifier    = 202
	ERROR                 = 500
	InvalidParams         = 400

	//成员错误
	ErrorExistNick          = 10001
	ErrorExistUser          = 10002
	ErrorNotExistUser       = 10003
	ErrorNotCompare         = 10004
	ErrorNotComparePassword = 10005
	ErrorFailEncryption     = 10006
	ErrorNotExistProduct    = 10007
	ErrorNotExistAddress    = 10008
	ErrorExistFavorite      = 10009
	ErrorUserNotFound       = 10010

	//店家错误
	ErrorBossCheckTokenFail        = 20001
	ErrorBossCheckTokenTimeout     = 20002
	ErrorBossToken                 = 20003
	ErrorBoss                      = 20004
	ErrorBossInsufficientAuthority = 20005
	ErrorBossProduct               = 20006

	// 购物车
	ErrorProductExistCart = 20007
	ErrorProductMoreCart  = 20008

	//管理员错误
	ErrorAuthCheckTokenFail        = 30001 //token 错误
	ErrorAuthCheckTokenTimeout     = 30002 //token 过期
	ErrorAuthToken                 = 30003
	ErrorAuth                      = 30004
	ErrorAuthInsufficientAuthority = 30005
	ErrorReadFile                  = 30006
	ErrorSendEmail                 = 30007
	ErrorCallApi                   = 30008
	ErrorUnmarshalJson             = 30009
	ErrorAdminFindUser             = 30010
	//数据库错误
	ErrorDatabase = 40001

	//对象存储错误
	ErrorOss        = 50001
	ErrorUploadFile = 50002

	// 幂等
	ErrIdempotencyTokenInvalid = 60001
	ErrIdempotencyInProgress   = 60002

	// 限流与熔断
	ErrRateLimitExceeded = 70001
	ErrCircuitOpen       = 70002

	// 满减 / 阶梯折扣引擎
	PromoRuleExpired       = 80001 // 规则已过期 / 未生效
	PromoRuleNotApplicable = 80002 // 购物车未达门槛 / 范围不匹配
	PromoBudgetExhausted   = 80003 // 平台当日预算用尽

	// 拼团 / 团购
	ErrGroupbuyFull          = 81001 // 团已满员
	ErrGroupbuyExpired       = 81002 // 团已超时
	ErrGroupbuyDuplicateJoin = 81003 // 同一用户重复加入
	ErrGroupbuyClosed        = 81004 // 团已关闭 (成团 / 散团 / 人工关)

	// 预售 / 两段式支付
	ErrPreorderNotInDepositWindow = 82001 // 不在定金期，付定金 / 取消全退被拒
	ErrPreorderNotInFinalWindow   = 82002 // 不在尾款期，付尾款被拒
	ErrPreorderDepositNotPaid     = 82003 // 尾款必须先付定金
	ErrPreorderForfeitedDeposit   = 82004 // 定金已扣（业务承诺：预售结束后不退）
)
