-- =============================================================================
-- 002_add_indexes.sql : コード上に実在するクエリに対してだけ索引を追加する。
--
-- 【原則: 左端プレフィックス則】複合索引 (主キー含む) は左端の列から順にしか
-- 使えない。よって複合主キーの 2 列目だけを条件にするクエリ (likes/reposts の
-- post_id、follows の followee_id) は主キーを使えず全表走査になる。以下の列順は
-- すべて「等価条件 → 範囲/GROUP BY キー → ソートキー → タイブレーカー id」で
-- 統一している。InnoDB の二次索引は主キー列を暗黙に含むためカバリングになる。
--
-- 【デプロイ上の罠 — 必読】docker-compose.yml:24 は ./migrations を
-- /docker-entrypoint-initdb.d/ にマウントするが、そこの SQL が実行されるのは
-- データボリューム (mariadb_data) の初回初期化時だけ。既存 DB に対して本ファイルは
-- 黙って読み飛ばされる ―「compose を up し直した」は適用の証明にならない。
--   (A) 稼働中 DB へ手動適用 (データ保持。通常はこちら) ※ app/backend/ で実行:
--       docker compose exec -T db \
--         mysql -u sakuravel -ppassword sakuravel < migrations/002_add_indexes.sql
--   (B) ボリュームごと作り直して 001 → 002 を流し直す (**全データが消える**):
--       docker compose down -v && docker compose up -d db
-- CREATE INDEX IF NOT EXISTS のみを使うため (A) は何度実行しても安全 (冪等)。
--
-- 【効果測定】効いていると仮定せず、適用前後で対象クエリを EXPLAIN し、type が
-- ALL から ref/range になり filesort/temporary が消えたかを本番相当データで確認する。
-- =============================================================================


-- likes -----------------------------------------------------------------------
-- post_id 等価 + 24h 窓の結合: like.go:16,71,91 / handler.go:106 (投稿描画ごとに発火) / post.go:31
CREATE INDEX IF NOT EXISTS idx_likes_post_created ON likes (post_id, created_at);

-- trending.go:13-14 は「直近 1 時間」の created_at 範囲で駆動するため上の索引では引けない (重複ではない)
CREATE INDEX IF NOT EXISTS idx_likes_created_post ON likes (created_at, post_id);

-- 追加しない (PRIMARY KEY (user_id, post_id) で完全一致できるため):
--   handler.go:117 EXISTS / like.go:86 DELETE / like.go:56 INSERT ... ON DUPLICATE KEY UPDATE


-- reposts ---------------------------------------------------------------------
-- post_id 等価の件数集計: repost.go:40,64 / handler.go:110 (投稿描画ごとに発火)
CREATE INDEX IF NOT EXISTS idx_reposts_post ON reposts (post_id);

-- 追加しない (PRIMARY KEY (user_id, post_id) で完全一致できるため):
--   handler.go:122 EXISTS / repost.go:55 DELETE


-- follows ---------------------------------------------------------------------
-- followee_id 等価 (フォロワー一覧・フォロワー数): user.go:82 / handler.go:65 (プロフィール表示ごと)
CREATE INDEX IF NOT EXISTS idx_follows_followee ON follows (followee_id);

-- 追加しない (PRIMARY KEY (follower_id, followee_id) の左端または完全一致で解決):
--   user.go:118 / handler.go:69 / post.go:43,86 / handler.go:78 EXISTS / user.go:178 DELETE


-- posts -----------------------------------------------------------------------
-- parent_post_id 絞り込み + created_at DESC 整列 (トップページの最重量経路):
--   post.go:23-25,32-34,41-45,79,83-86 / handler.go:161 / reply.go:112-114
CREATE INDEX IF NOT EXISTS idx_posts_parent_created_id
    ON posts (parent_post_id, created_at, id);

-- user_id 絞り込み + 整列 (ユーザーページ): post.go:148-149,174 / handler.go:73 (左端のみ利用)
-- 注意: type=replies の parent_post_id IS NOT NULL は範囲条件なので整列は索引順で満たせず
--       filesort が残る (user_id の絞り込み効果のみ)。EXPLAIN で実測すること。
CREATE INDEX IF NOT EXISTS idx_posts_user_parent_created_id
    ON posts (user_id, parent_post_id, created_at, id);

-- 追加しない:
--   repost.go:58 DELETE ... user_id = ? AND original_post_id = ?
--     → 上の索引の左端 user_id で絞れる。リポスト取り消しは低頻度。
--   handler.go:91 / like.go:64 / repost.go:33 / reply.go:30 / notification.go:65,71 (WHERE id = ?)
--     → PRIMARY KEY (id) で解決。
--   search.go:34-36,62 WHERE content LIKE ? → search.go:32 が "%q%" と前方ワイルドカードを付ける
--     ため B-Tree では引けない。FULLTEXT / 外部全文検索が必要な別課題。


-- notifications ---------------------------------------------------------------
-- user_id 等価 + created_at DESC 整列 (通知一覧・全件): notification.go:24-28,99
CREATE INDEX IF NOT EXISTS idx_notifications_user_created_id
    ON notifications (user_id, created_at, id);

-- type 絞り込み時 (notification.go:19 の typeCond): notification.go:26,99 — 等価 2 列で整列も索引順
CREATE INDEX IF NOT EXISTS idx_notifications_user_type_created_id
    ON notifications (user_id, type, created_at, id);

-- 未読バッジ (画面表示ごとの高頻度): notification.go:92,125 COUNT / notification.go:115 UPDATE
CREATE INDEX IF NOT EXISTS idx_notifications_user_read
    ON notifications (user_id, is_read);


-- footprints ------------------------------------------------------------------
-- user_id 等価 + visitor_id での GROUP BY (一時テーブル回避) + MAX(created_at) カバリング:
--   footprint.go:26-31,65 ※ 集計後の ORDER BY last_visited の filesort は残る
CREATE INDEX IF NOT EXISTS idx_footprints_user_visitor_created
    ON footprints (user_id, visitor_id, created_at);


-- sessions / users : 追加なし ---------------------------------------------------
--   middleware/auth.go:27,50 / auth.go:153 → sessions.id は 001_init.sql:12 で PRIMARY KEY
--     (全リクエストが通るホットパスだが主キー完全一致なのでこれ以上速くならない)。
--   auth.go:112 WHERE email = ? → users.email は 001_init.sql:5 で UNIQUE。
--   handler.go:55,132 → users.id は 001_init.sql:2 で PRIMARY KEY。
--   search.go:76-78,104 username/display_name LIKE ? → search.go:74 が "%q%" と前方
--     ワイルドカードを付けるため B-Tree では利用不可。索引を足しても無意味。
