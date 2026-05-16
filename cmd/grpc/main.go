package main

import (
	"flag"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	conf "github.com/RedInn7/gomall/config"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	userpb "github.com/RedInn7/gomall/proto/user"
	"github.com/RedInn7/gomall/repository/db/dao"
	grpcsvc "github.com/RedInn7/gomall/service/grpc"
)

func main() {
	addr := flag.String("addr", ":9000", "gRPC server listen addr")
	flag.Parse()

	conf.InitConfig()
	dao.InitMySQL()
	util.InitLog()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		panic(err)
	}
	srv := grpc.NewServer()
	userpb.RegisterUserServiceServer(srv, &grpcsvc.UserServer{})
	reflection.Register(srv)

	fmt.Printf("gRPC server listening on %s\n", *addr)
	if err := srv.Serve(lis); err != nil {
		panic(err)
	}
}
