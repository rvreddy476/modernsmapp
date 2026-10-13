package partnerwork

import (
	"context"
	"errors"
	"fmt"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type revisionKey struct{}

// RevisionMiddleware carries the revision the app just read into the domain's
// existing duty endpoint, without changing that endpoint's approval checks.
func RevisionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if v := c.GetHeader("X-Work-Revision"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": "WORK_REVISION_INVALID"})
				return
			}
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), revisionKey{}, n))
		}
		c.Next()
	}
}

func DomainDutyTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, category Category, on bool) error {
	if !Enabled(os.Getenv) {
		return nil
	}
	if v, ok := ctx.Value(revisionKey{}).(int64); ok {
		if _, err := tx.Exec(ctx, `SELECT set_config('partner_work.expected_revision',$1,true)`, strconv.FormatInt(v, 10)); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `SELECT partner_work.domain_duty($1,$2,$3)`, user, string(category), on)
	if err != nil {
		return Translate(err)
	}
	_, err = tx.Exec(ctx, `SELECT set_config('partner_work.expected_revision','',true)`)
	return err
}

// Translate gives Go callers the same stable errors as the remote coordinator.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	var p *pgconn.PgError
	if errors.As(err, &p) && (p.Code == "40P01" || p.Code == "40001") {
		return ErrStale
	}
	if errors.As(err, &p) && p.Code == "P0001" && strings.HasPrefix(p.Message, "partner_work:") {
		for code, e := range map[string]error{"STALE": ErrStale, "BUSY": ErrBusy, "CONFLICT": ErrConflict, "OFF_DUTY": ErrOffDuty, "EXPIRED": ErrExpired} {
			if p.Message == "partner_work:"+code {
				return fmt.Errorf("%w", e)
			}
		}
	}
	return err
}

func WriteError(c *gin.Context, err error) bool {
	e := Translate(err)
	code := ErrorCode(e)
	if e == nil || code == "UNAVAILABLE" {
		return false
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "WORK_"+code, e.Error(), nil)
	return true
}

func CandidateUnavailable(err error) bool {
	e := Translate(err)
	return errors.Is(e, ErrConflict) || errors.Is(e, ErrBusy) || errors.Is(e, ErrOffDuty) || errors.Is(e, ErrExpired) || errors.Is(e, ErrStale)
}
