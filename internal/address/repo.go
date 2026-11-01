package address

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

// ErrAddressNotOwned 地址不存在或不属于当前用户：下单类入口据此拒单。
var ErrAddressNotOwned = errors.New("收货地址不存在或不属于当前用户")

type AddressDao struct {
	*gorm.DB
}

func NewAddressDao(ctx context.Context) *AddressDao {
	return &AddressDao{dao.NewDBClient(ctx)}
}

func NewAddressDaoByDB(db *gorm.DB) *AddressDao {
	return &AddressDao{db}
}

// GetAddressByAid 根据 AddressId 获取 Address
func (d *AddressDao) GetAddressByAid(aId, uId uint) (address *Address, err error) {
	err = d.DB.Model(&Address{}).
		Where("id = ? AND user_id = ?", aId, uId).First(&address).
		Error

	return
}

// OwnedSnapshot 校验收货地址归属并返回下单用的地址快照：addressID 必须存在且属于 userID，否则返回 ErrAddressNotOwned。
// address_id 同样是客户端可篡改的字段，下单类入口不能只信请求体——否则用户可拿别人的地址 id
// 下单，把货寄到他人地址或撞出他人隐私。addressID 为 0 视为"未选地址"，返回空快照，是否必填由各域自行决定。
// 订单必须存快照而不是只存 address_id：地址可改可删，只存 id 会让历史订单的收货地址跟着变。
func (d *AddressDao) OwnedSnapshot(addressID, userID uint) (Snapshot, error) {
	if addressID == 0 {
		return Snapshot{}, nil
	}
	if d == nil || d.DB == nil {
		return Snapshot{}, errors.New("address dao 不可用，无法校验地址归属")
	}
	addr, err := d.GetAddressByAid(addressID, userID)
	if err != nil || addr == nil || addr.ID == 0 {
		return Snapshot{}, ErrAddressNotOwned
	}
	return Snapshot{Name: addr.Name, Phone: addr.Phone, Detail: addr.Address}, nil
}

func (d *AddressDao) GetAddressByuId(uId uint) (address []*Address, err error) {
	err = d.DB.Model(&Address{}).
		Where("user_id = ?", uId).Find(&address).
		Error

	return
}

// ListAddressByUid 按用户 id 倒序取出地址列表
func (d *AddressDao) ListAddressByUid(uid uint) (r []*AddressResp, err error) {
	err = d.DB.Model(&Address{}).
		Where("user_id = ?", uid).
		Order("created_at desc").
		Select("id, user_id, name, phone, address, UNIX_TIMESTAMP(created_at) AS created_at").
		Find(&r).Error

	return
}

func (d *AddressDao) CreateAddress(address *Address) (err error) {
	return d.DB.Model(&Address{}).
		Create(&address).Error
}

// DeleteAddressById 删除自己的地址。id 不存在或不属于 uId 时返回 ErrAddressNotOwned，不再静默成功。
// 订单持有下单时的地址快照，删地址不影响历史订单。
func (d *AddressDao) DeleteAddressById(aId, uId uint) error {
	res := d.DB.Where("id = ? AND user_id = ?", aId, uId).Delete(&Address{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAddressNotOwned
	}
	return nil
}

// UpdateAddressById 修改自己的地址。先查归属再改：MySQL 的 RowsAffected 只数真正变了的行，
// 提交相同内容会是 0，不能拿它判归属。Select 显式列出字段，Updates(struct) 才不会跳过零值。
func (d *AddressDao) UpdateAddressById(aId, uId uint, address *Address) error {
	if a, err := d.GetAddressByAid(aId, uId); err != nil || a == nil || a.ID == 0 {
		return ErrAddressNotOwned
	}
	return d.DB.Model(&Address{}).
		Where("id = ? AND user_id = ?", aId, uId).
		Select("name", "phone", "address").
		Updates(address).Error
}
