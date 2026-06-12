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

func (s *AddressSrv) AddressCreate(ctx context.Context, req *AddressCreateReq) (resp interface{}, err error) {
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
		return
	}
	return
}

func (s *AddressSrv) AddressShow(ctx context.Context) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	address, err := NewAddressDao(ctx).GetAddressByuId(u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	resp = &types.DataListResp{
		Item:  address,
		Total: int64(len(address)),
	}

	return
}

func (s *AddressSrv) AddressList(ctx context.Context, req *AddressListReq) (resp interface{}, err error) {
	u, _ := ctl.GetUserInfo(ctx)
	resp, err = NewAddressDao(ctx).
		ListAddressByUid(u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}
	return
}

func (s *AddressSrv) AddressDelete(ctx context.Context, req *AddressDeleteReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	err = NewAddressDao(ctx).DeleteAddressById(req.Id, u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	return
}

func (s *AddressSrv) AddressUpdate(ctx context.Context, req *AddressServiceReq) (resp interface{}, err error) {
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
		return
	}
	var addresses []*AddressResp
	addresses, err = addressDao.ListAddressByUid(u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	resp = &types.DataListResp{
		Item:  addresses,
		Total: int64(len(addresses)),
	}

	return
}
