-- +goose Up
-- Page views of people per hour and day (the top pages only keep 30 days of
-- days, and no hours), and how fast the app answered people: a count per
-- answer time bucket, under 50 ms, 100 ms, 200 ms, 500 ms, 1 s, 2 s, 5 s and
-- slower (traffic.SpeedBounds). Counts only, like the rest.
ALTER TABLE traffic_hourly ADD COLUMN views INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_0 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_1 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_2 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_3 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_4 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_5 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_6 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_hourly ADD COLUMN speed_7 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN views INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_0 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_1 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_2 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_3 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_4 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_5 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_6 INTEGER NOT NULL DEFAULT 0;
ALTER TABLE traffic_daily ADD COLUMN speed_7 INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE traffic_daily DROP COLUMN speed_7;
ALTER TABLE traffic_daily DROP COLUMN speed_6;
ALTER TABLE traffic_daily DROP COLUMN speed_5;
ALTER TABLE traffic_daily DROP COLUMN speed_4;
ALTER TABLE traffic_daily DROP COLUMN speed_3;
ALTER TABLE traffic_daily DROP COLUMN speed_2;
ALTER TABLE traffic_daily DROP COLUMN speed_1;
ALTER TABLE traffic_daily DROP COLUMN speed_0;
ALTER TABLE traffic_daily DROP COLUMN views;
ALTER TABLE traffic_hourly DROP COLUMN speed_7;
ALTER TABLE traffic_hourly DROP COLUMN speed_6;
ALTER TABLE traffic_hourly DROP COLUMN speed_5;
ALTER TABLE traffic_hourly DROP COLUMN speed_4;
ALTER TABLE traffic_hourly DROP COLUMN speed_3;
ALTER TABLE traffic_hourly DROP COLUMN speed_2;
ALTER TABLE traffic_hourly DROP COLUMN speed_1;
ALTER TABLE traffic_hourly DROP COLUMN speed_0;
ALTER TABLE traffic_hourly DROP COLUMN views;
