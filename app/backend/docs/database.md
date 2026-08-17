## データベース設計

### テーブル一覧

| テーブル | 説明 |
|---|---|
| `users` | ユーザー情報 |
| `sessions` | セッション管理 |
| `posts` | 投稿（リポスト・返信を含む。返信は `parent_post_id` で返信先を指す） |
| `follows` | フォロー関係 |
| `likes` | いいね |
| `reposts` | リポスト |
| `footprints` | プロフィール訪問履歴 |
| `notifications` | 通知（いいね・フォロー・リポスト・返信・足跡） |

### DDL

[`../migrations/001_init.sql`](../migrations/001_init.sql) を参照
