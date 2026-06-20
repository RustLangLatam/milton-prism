package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/proto"
)

func HttpResponseModifier(ctx context.Context, w http.ResponseWriter, p proto.Message) error {
	md, ok := runtime.ServerMetadataFromContext(ctx)
	if !ok {
		return nil
	}

	// set http status code
	if vals := md.HeaderMD.Get("x-http-code"); len(vals) > 0 {
		code, err := strconv.Atoi(vals[0])
		if err != nil {
			return err
		}
		// delete the headers to not expose any grpc-metadata in http response
		delete(md.HeaderMD, "x-http-code")
		delete(w.Header(), "Grpc-Metadata-X-Http-Status")
		w.WriteHeader(code)
	}

	//switch p.(type) {

	//}

	return nil
}

func IncomingHeaderMatcher(key string) (string, bool) {
	switch strings.ToLower(key) {
	case "authorization":
		return "", false
	case "refresh":
		return "x-refresh-token", true
	default:
		return runtime.DefaultHeaderMatcher(key)
	}
}

func OutgoingHeaderMatcher(key string) (string, bool) {
	switch strings.ToLower(key) {
	case "content-disposition":
		return "Content-Disposition", true
	default:
		return runtime.DefaultHeaderMatcher(key)
	}
}
