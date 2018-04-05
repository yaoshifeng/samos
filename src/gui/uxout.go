package gui

import (
	"net/http"

	"github.com/samoslab/samos/src/cipher"
	wh "github.com/samoslab/samos/src/util/http" //http,json helpers
	"github.com/samoslab/samos/src/visor/historydb"
)

func getUxOutByID(gateway Gatewayer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			wh.Error405(w)
			return
		}

		uxid := r.FormValue("uxid")
		if uxid == "" {
			wh.Error400(w, "uxid is empty")
			return
		}

		id, err := cipher.SHA256FromHex(uxid)
		if err != nil {
			wh.Error400(w, err.Error())
			return
		}

		uxout, err := gateway.GetUxOutByID(id)
		if err != nil {
			wh.Error400(w, err.Error())
			return
		}

		if uxout == nil {
			wh.Error404(w)
			return
		}

		wh.SendOr404(w, historydb.NewUxOutJSON(uxout))
	}
}

func getAddrUxOuts(gateway Gatewayer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			wh.Error405(w)
			return
		}
		addr := r.FormValue("address")
		if addr == "" {
			wh.Error400(w, "address is empty")
			return
		}

		cipherAddr, err := cipher.DecodeBase58Address(addr)
		if err != nil {
			wh.Error400(w, err.Error())
			return
		}

		uxs, err := gateway.GetAddrUxOuts(cipherAddr)
		if err != nil {
			wh.Error400(w, err.Error())
			return
		}

		wh.SendOr404(w, uxs)
	}
}
