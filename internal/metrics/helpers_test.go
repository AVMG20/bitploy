package metrics

import (
	"context"

	"github.com/AVMG20/bitploy/internal/store/db"
)

type dbQ = db.Queries

func insertSample(ctx context.Context, q *db.Queries, app string, ts int64, cpu float64, mem, rx, tx int64) error {
	return q.OpsInsertSample(ctx, db.OpsInsertSampleParams{AppID: app, Ts: ts, Cpu: cpu, MemBytes: mem, RxBytes: rx, TxBytes: tx})
}
