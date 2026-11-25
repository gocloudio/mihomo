package route

import (
	"strconv"

	"github.com/metacubex/mihomo/tunnel"

	"github.com/metacubex/chi"
	"github.com/metacubex/chi/render"
	"github.com/metacubex/http"
)

// updatePIDRoute 通过 PUT 设置绑定，通过 DELETE 幂等删除绑定，两种操作成功时均返回 204。
func updatePIDRoute(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 32)
	if err != nil || pid <= 0 {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("Invalid PID"))
		return
	}
	if r.Method == http.MethodDelete {
		tunnel.DeletePIDRoute(int32(pid))
		render.NoContent(w, r)
		return
	}
	var req struct {
		Proxy string `json:"proxy"`
	}
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, ErrBadRequest)
		return
	}
	if err := tunnel.SetPIDRoute(int32(pid), req.Proxy); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError(err.Error()))
		return
	}
	render.NoContent(w, r)
}
