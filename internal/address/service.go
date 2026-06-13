package address

import (
	"context"
	"sync"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/types"
)

var AddressSrvIns *AddressSrv
var AddressSrvOnce sync.Once

type AddressSrv struct {
}

func GetAddressSrv() *AddressSrv {
	AddressSrvOnce.Do(func() {
		AddressSrvIns = &AddressSrv{}
	})
	return AddressSrvIns
}

// AddressCreate 新增收货地址。成功不回传地址体（保持既有 API 契约：data 为 null）。
func (s *AddressSrv) AddressCreate(ctx context.Context, req *AddressCreateReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	addressDao := NewAddressDao(ctx)
	address := &Address{
		UserID:  u.Id,
		Name:    req.Name,
		Phone:   req.Phone,
		Address: req.Address,
	}
	err = addressDao.CreateAddress(address)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	return nil, nil
}

func (s *AddressSrv) AddressShow(ctx context.Context) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	address, err := NewAddressDao(ctx).GetAddressByuId(u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return &types.DataListResp{
		Item:  address,
		Total: int64(len(address)),
	}, nil
}

func (s *AddressSrv) AddressList(ctx context.Context, req *AddressListReq) ([]*AddressResp, error) {
	u, _ := ctl.GetUserInfo(ctx)
	resp, err := NewAddressDao(ctx).
		ListAddressByUid(u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	return resp, nil
}

// AddressDelete 删除收货地址。成功不回传数据（保持既有 API 契约：data 为 null）。
func (s *AddressSrv) AddressDelete(ctx context.Context, req *AddressDeleteReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	err = NewAddressDao(ctx).DeleteAddressById(req.Id, u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

func (s *AddressSrv) AddressUpdate(ctx context.Context, req *AddressServiceReq) (*types.DataListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	addressDao := NewAddressDao(ctx)
	address := &Address{
		UserID:  u.Id,
		Name:    req.Name,
		Phone:   req.Phone,
		Address: req.Address,
	}
	err = addressDao.UpdateAddressById(req.Id, u.Id, address)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	addresses, err := addressDao.ListAddressByUid(u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return &types.DataListResp{
		Item:  addresses,
		Total: int64(len(addresses)),
	}, nil
}
