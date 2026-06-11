package address

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

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
		Select("id, user_id, name, phone, address, UNIX_TIMESTAMP(created_at)").
		Find(&r).Error

	return
}

func (d *AddressDao) CreateAddress(address *Address) (err error) {
	return d.DB.Model(&Address{}).
		Create(&address).Error
}

func (d *AddressDao) DeleteAddressById(aId, uId uint) (err error) {
	return d.DB.Where("id = ? AND user_id = ?", aId, uId).
		Delete(&Address{}).Error
}

func (d *AddressDao) UpdateAddressById(aId uint, address *Address) (err error) {
	return d.DB.Model(&Address{}).
		Where("id=?", aId).
		Updates(address).Error
}
