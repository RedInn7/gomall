package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userpb "github.com/RedInn7/gomall/proto/user"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
)

// UserServer 实现 user.UserService
type UserServer struct {
	userpb.UnimplementedUserServiceServer
}

func (s *UserServer) GetUser(ctx context.Context, req *userpb.GetUserRequest) (*userpb.UserInfo, error) {
	if req.Id == 0 {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	u, err := dao.NewUserDao(ctx).GetUserById(uint(req.Id))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if u == nil || u.ID == 0 {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	return toPB(u), nil
}

func (s *UserServer) ListUsers(ctx context.Context, req *userpb.ListUsersRequest) (*userpb.ListUsersResponse, error) {
	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	var (
		users []*model.User
		total int64
	)
	db := dao.NewUserDao(ctx).DB
	if err := db.Model(&model.User{}).Count(&total).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := db.Model(&model.User{}).
		Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&users).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	items := make([]*userpb.UserInfo, 0, len(users))
	for _, u := range users {
		items = append(items, toPB(u))
	}
	return &userpb.ListUsersResponse{Items: items, Total: total}, nil
}

func toPB(u *model.User) *userpb.UserInfo {
	return &userpb.UserInfo{
		Id:        uint32(u.ID),
		UserName:  u.UserName,
		NickName:  u.NickName,
		Email:     u.Email,
		Status:    u.Status,
		CreatedAt: u.CreatedAt.Unix(),
	}
}
