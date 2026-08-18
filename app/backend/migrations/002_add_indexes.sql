-- 002: コード上に実在するクエリに対してだけ索引を追加する。列順はどれも左端プレフィックス則に
-- 従い「等価条件 → 範囲/GROUP BY キー → ソートキー → タイブレーカー id」で統一している。
-- 【注意】initdb マウント経由の SQL はボリューム初回作成時にしか走らず、既存 DB では
-- 黙って読み飛ばされる。手動適用すること（何度実行しても安全）:
--   docker compose exec -T db mysql -u sakuravel -ppassword sakuravel < migrations/002_add_indexes.sql

-- likes: post_id 等価 + 24h 窓の結合（投稿描画ごとに発火）
CREATE INDEX IF NOT EXISTS idx_likes_post_created ON likes (post_id, created_at);
-- likes: trending.go の「直近 1 時間」は created_at 範囲で駆動するため上の索引では引けない
CREATE INDEX IF NOT EXISTS idx_likes_created_post ON likes (created_at, post_id);
-- reposts: post_id 等価の件数集計（投稿描画ごとに発火）
CREATE INDEX IF NOT EXISTS idx_reposts_post ON reposts (post_id);
-- follows: followee_id 等価（フォロワー一覧・フォロワー数）
CREATE INDEX IF NOT EXISTS idx_follows_followee ON follows (followee_id);
-- posts: parent_post_id 絞り込み + created_at DESC 整列（トップページの最重量経路）
CREATE INDEX IF NOT EXISTS idx_posts_parent_created_id
    ON posts (parent_post_id, created_at, id);
-- posts: user_id 絞り込み + 整列（ユーザーページ。type=replies は範囲条件なので filesort が残る）
CREATE INDEX IF NOT EXISTS idx_posts_user_parent_created_id
    ON posts (user_id, parent_post_id, created_at, id);
-- notifications: 通知一覧（user_id 等価 + created_at DESC 整列）
CREATE INDEX IF NOT EXISTS idx_notifications_user_created_id
    ON notifications (user_id, created_at, id);
-- notifications: 種別で絞り込んだ通知一覧（等価 2 列なので整列も索引順）
CREATE INDEX IF NOT EXISTS idx_notifications_user_type_created_id
    ON notifications (user_id, type, created_at, id);
-- notifications: 未読バッジ（画面表示ごとの高頻度）
CREATE INDEX IF NOT EXISTS idx_notifications_user_read
    ON notifications (user_id, is_read);
-- footprints: user_id 等価 + visitor_id の GROUP BY（一時テーブル回避）+ MAX(created_at) カバリング
CREATE INDEX IF NOT EXISTS idx_footprints_user_visitor_created
    ON footprints (user_id, visitor_id, created_at);

-- 意図的に追加していないもの: likes / reposts / follows の完全一致・左端一致は各主キーで、
-- posts / users / sessions の id 検索と email 検索は主キーと UNIQUE で解決できる。
-- search.go の LIKE "%q%" は前方ワイルドカードのため B-Tree では引けない（別課題）。
