-- 006: 「参照先が消えたらこの行は無意味」が自明な関係だけを FK 化する。すべて ON DELETE CASCADE。
-- posts.parent_post_id は意図的に除外。投稿削除が他人の返信も消すべきかは未決の仕様判断だから。
-- FOREIGN KEY IF NOT EXISTS（MariaDB 拡張。IF NOT EXISTS は CONSTRAINT ではなく FOREIGN KEY 側に付く）
-- なので適用済み DB への再実行は無害（CD は毎回全ファイルを流す）。
-- 投稿削除は子行を消さないため既存 DB には孤児が居る。孤児が 1 行でもあると初回の ALTER は落ち、
-- 部分適用のまま残る。新規 DB へ流すこと（開発中は scripts/seed.sh --reset で作り直す）。
-- 手動適用: docker compose exec -T db mysql -u sakuravel -ppassword sakuravel < migrations/006_fk_constraints.sql

ALTER TABLE sessions ADD CONSTRAINT fk_sessions_user FOREIGN KEY IF NOT EXISTS (user_id) REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE posts ADD CONSTRAINT fk_posts_user FOREIGN KEY IF NOT EXISTS (user_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_posts_original FOREIGN KEY IF NOT EXISTS (original_post_id) REFERENCES posts (id) ON DELETE CASCADE;
ALTER TABLE likes ADD CONSTRAINT fk_likes_user FOREIGN KEY IF NOT EXISTS (user_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_likes_post FOREIGN KEY IF NOT EXISTS (post_id) REFERENCES posts (id) ON DELETE CASCADE;
ALTER TABLE reposts ADD CONSTRAINT fk_reposts_user FOREIGN KEY IF NOT EXISTS (user_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_reposts_post FOREIGN KEY IF NOT EXISTS (post_id) REFERENCES posts (id) ON DELETE CASCADE;
ALTER TABLE follows ADD CONSTRAINT fk_follows_follower FOREIGN KEY IF NOT EXISTS (follower_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_follows_followee FOREIGN KEY IF NOT EXISTS (followee_id) REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE footprints ADD CONSTRAINT fk_footprints_user FOREIGN KEY IF NOT EXISTS (user_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_footprints_visitor FOREIGN KEY IF NOT EXISTS (visitor_id) REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE notifications ADD CONSTRAINT fk_notifications_user FOREIGN KEY IF NOT EXISTS (user_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_notifications_actor FOREIGN KEY IF NOT EXISTS (actor_id) REFERENCES users (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_notifications_post FOREIGN KEY IF NOT EXISTS (post_id) REFERENCES posts (id) ON DELETE CASCADE;
