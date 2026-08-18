-- 004: SSE の fan-out 用中継表。配信のきっかけを 1 行書き、各プロセスがこれを追って
-- 自分のサブスクライバーにだけ配る。本文は載せず、サブスクライバーを抱えているプロセスが id から引き直す。
--   kind='notification'  subject_id = 宛先の user_id
--   kind='reply'         subject_id = スレッドの根の post_id
-- created_at が TIMESTAMP(6) なのは、秒精度だとポーラの 1 秒消費ラグが丸めで 1〜2 秒に広がるため。
-- 既存 DB では読み飛ばされるので手動適用する: docker compose exec -T db mysql -u sakuravel -ppassword sakuravel < migrations/004_sse_events.sql
CREATE TABLE IF NOT EXISTS sse_events (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    kind       VARCHAR(20)  NOT NULL,  -- 配信先の種別。ポーラはこれで配る hub を選ぶ
    subject_id BIGINT       NOT NULL,  -- 配信先の鍵。意味は kind ごとに異なる（上記）
    type       VARCHAR(20),            -- kind='notification' でのみ意味を持つ（SSE 本文の type）
    post_id    BIGINT,
    created_at TIMESTAMP(6) NOT NULL DEFAULT NOW(6),
    KEY idx_sse_events_created (created_at)  -- GC の DELETE を範囲走査にする
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
