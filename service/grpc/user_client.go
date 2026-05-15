package grpc

import (
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	userpb "github.com/RedInn7/gomall/proto/user"
)

var (
	userClient     userpb.UserServiceClient
	userClientOnce sync.Once
	userConn       *grpc.ClientConn
)

// GetUserClient 拿到 user 服务的 gRPC 客户端单例，默认连本机 :9000
// 通过 InitUserClient 可以指定地址
func GetUserClient() (userpb.UserServiceClient, error) {
	if userClient != nil {
		return userClient, nil
	}
	return InitUserClient("127.0.0.1:9000")
}

func InitUserClient(target string) (userpb.UserServiceClient, error) {
	var err error
	userClientOnce.Do(func() {
		userConn, err = grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return
		}
		userClient = userpb.NewUserServiceClient(userConn)
	})
	return userClient, err
}

func CloseUserClient() error {
	if userConn != nil {
		return userConn.Close()
	}
	return nil
}
