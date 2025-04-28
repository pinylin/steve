package handler

import (
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/rancher/apiserver/pkg/server"
	apiserver "github.com/rancher/apiserver/pkg/server"
	"github.com/rancher/apiserver/pkg/types"
	"github.com/rancher/apiserver/pkg/urlbuilder"
	"github.com/rancher/steve/pkg/accesscontrol"
	"github.com/rancher/steve/pkg/auth"
	k8sproxy "github.com/rancher/steve/pkg/proxy"
	"github.com/rancher/steve/pkg/schema"
	"github.com/rancher/steve/pkg/server/router"
	"github.com/sirupsen/logrus"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/rest"
)

func New(cfg *rest.Config, sf schema.Factory, authMiddleware auth.Middleware, next http.Handler,
	routerFunc router.RouterFunc) (*apiserver.Server, http.Handler, error) {
	var (
		proxy http.Handler
		err   error
	)

	a := &apiServer{
		sf:     sf,
		server: server.DefaultAPIServer(),
	}
	a.server.AccessControl = accesscontrol.NewAccessControl()

	if authMiddleware == nil {
		proxy, err = k8sproxy.Handler("/", cfg)
		if err != nil {
			return a.server, nil, err
		}
		authMiddleware = auth.ToMiddleware(auth.AuthenticatorFunc(auth.AlwaysAdmin))
	} else {
		proxy = k8sproxy.ImpersonatingHandler("/", cfg)
	}

	w := authMiddleware
	handlers := router.Handlers{
		Next:        next,
		K8sResource: w(a.apiHandler(k8sAPI)),
		K8sProxy:    w(proxy),
		APIRoot:     w(a.apiHandler(apiRoot)),
	}
	if routerFunc == nil {
		return a.server, router.Routes(handlers), nil
	}
	return a.server, routerFunc(handlers), nil
}

type apiServer struct {
	sf     schema.Factory
	server *server.Server
}

func (a *apiServer) common(rw http.ResponseWriter, req *http.Request) (*types.APIRequest, bool) {
	user, ok := request.UserFrom(req.Context())
	if !ok {
		return nil, false
	}

	logrus.Infof("[apiServer.common] 1: flag: %s, time: %s", req.Header.Get("flag"), time.Now().String())
	schemas, err := a.sf.Schemas(user)
	if err != nil {
		logrus.Errorf("HTTP request failed: %v", err)
		rw.Write([]byte(err.Error()))
		rw.WriteHeader(http.StatusInternalServerError)
	}
	logrus.Infof("[apiServer.common] 2: flag: %s, time: %s", req.Header.Get("flag"), time.Now().String())

	urlBuilder, err := urlbuilder.NewPrefixed(req, schemas, "v1")
	if err != nil {
		rw.Write([]byte(err.Error()))
		rw.WriteHeader(http.StatusInternalServerError)
		return nil, false
	}

	return &types.APIRequest{
		Schemas:    schemas,
		Request:    req,
		Response:   rw,
		URLBuilder: urlBuilder,
	}, true
}

type APIFunc func(schema.Factory, *types.APIRequest)

func (a *apiServer) apiHandler(apiFunc APIFunc) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rand.Seed(time.Now().UnixNano())
		id := fmt.Sprintf("%08d", rand.Intn(100000000))
		req.Header.Set("flag", id)
		apiOp, ok := a.common(rw, req)
		if ok {
			if apiFunc != nil {
				apiFunc(a.sf, apiOp)
			}
			logrus.Infof("[apiHandler.Handle] 1: flag: %s, time: %s", id, time.Now().String())
			a.server.Handle(apiOp)
			logrus.Infof("[apiHandler.Handle] 2: flag: %s, time: %s", id, time.Now().String())
		}
	})
}
