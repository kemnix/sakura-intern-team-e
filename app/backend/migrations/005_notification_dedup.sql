-- 005: 通知の重複挿入ガード（handler.insertNotificationOnce の NOT EXISTS）用の索引。
-- UNIQUE にできないのは、follow / footprint の post_id が NULL で、一意索引が NULL 同士を
-- 別の値として扱うためである。NULL も等しいと判定できる <=> はクエリにしか書けない。
-- 既存 DB では読み飛ばされるので手動適用する: docker compose exec -T db mysql -u sakuravel -ppassword sakuravel < migrations/005_notification_dedup.sql
CREATE INDEX IF NOT EXISTS idx_notifications_dedup
    ON notifications (user_id, type, actor_id, post_id);
