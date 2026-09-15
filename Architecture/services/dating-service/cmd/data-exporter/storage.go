package main

import (
	"fmt"
	"strings"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
)

// exportStorageFromEnv picks where finished exports go (lane D9):
//
//	DATING_EXPORT_STORAGE=database (default) — sealed on the export row and
//	    downloaded by the owner from GET /v1/dating/data-export/:id/download.
//	    Needs DATING_PII_KEYS.
//	DATING_EXPORT_STORAGE=media — POST to media-service
//	    /v1/internal/exports/:id. That route does not exist in media-service
//	    today; keep this for when it does.
func exportStorageFromEnv(getenv func(string) string, st *store.Store) (service.ExportStorageClient, error) {
	switch mode := strings.ToLower(strings.TrimSpace(getenv("DATING_EXPORT_STORAGE"))); mode {
	case "", "database":
		if st.PII() == nil {
			return nil, fmt.Errorf("DATING_EXPORT_STORAGE=database needs DATING_PII_KEYS and DATING_PII_LOOKUP_SALT: exports are sealed at rest")
		}
		return service.NewDatabaseExportStorage(st), nil
	case "media":
		return newHTTPMediaExportStorage(), nil
	default:
		return nil, fmt.Errorf("DATING_EXPORT_STORAGE must be database or media")
	}
}
