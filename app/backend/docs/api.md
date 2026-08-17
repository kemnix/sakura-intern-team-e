## API 仕様

### 共通仕様

- リクエスト・レスポンス形式 `application/json`

- 認証方式: セッションクッキー
  - ログイン後 `Set-Cookie: session_id=<token>` を返す
  - 認証が必要なエンドポイントは `Cookie: session_id=<token>` を要求
  - 未認証時は `401 Unauthorized` を返す

- エラーレスポンス
```json
{
  "error": "エラーメッセージ"
}
```

- SSE エンドポイント（`/notifications/stream`・`/posts/:id/thread/stream`）

  この2本だけは JSON を1回返して終わりではなく、接続を維持してサーバー側からイベントを流し続ける（Server-Sent Events）。

  - レスポンスヘッダは `Content-Type: text/event-stream` / `Cache-Control: no-cache` / `Connection: keep-alive`
  - 本文は `data: <JSON>` の1行＋空行の繰り返し。JSON の形は `{"type": "...", "data": {...}}`
  - 25秒ごとに `: ping` というコメント行を送って接続を維持する（クライアントは無視してよい）
  - ブラウザ標準の `EventSource` で受け取れる（切断時の再接続は `EventSource` 側が行う）

```
data: {"type":"reply","data":{ /* Post オブジェクト */ }}

data: {"type":"notification","data":{"type":"like","post_id":42}}

: ping
```

  購読の単位は `/notifications/stream` がユーザーID、`/posts/:id/thread/stream` はスレッドのルート投稿ID
  （接続時に `parent_post_id` を辿って解決するため、スレッドのどの階層を指定しても同じスレッドの返信が届く）。

- ページネーション（タイムライン・検索）
```
GET /posts?page=1&per_page=20
```
レスポンスに以下を含む:
```json
{
  "posts": [...],
  "total": 1234,
  "page": 1,
  "per_page": 20
}
```

---

### データオブジェクト定義

#### User オブジェクト

```json
{
  "id": 1,
  "username": "aoi_haru",
  "display_name": "あおい",
  "bio": "自己紹介テキスト",
  "followers_count": 120,
  "following_count": 45,
  "post_count": 300,
  "avatar_color": "orange",
  "followed_by_me": false,
  "created_at": "2026-06-30T09:00:00Z"
}
```

`followed_by_me` は、ログイン中で相手が自分以外のときに実際の値が入る（それ以外は `false`）。

`avatar_color` は DB に保存せず `user.id % 5` で動的生成する。

| id % 5 | color |
|---|---|
| 0 | orange |
| 1 | blue |
| 2 | purple |
| 3 | mint |
| 4 | pink |

#### Post オブジェクト

```json
{
  "id": 1,
  "content": "今日もいい天気🌸",
  "created_at": "2026-06-30T09:00:00Z",
  "likes_count": 42,
  "reposts_count": 8,
  "replies_count": 7,
  "is_repost": false,
  "original_post_id": null,
  "parent_post_id": null,
  "author": { /* User オブジェクト */ },
  "liked_by_me": true,
  "reposted_by_me": false
}
```

リポストの場合は `original_post` に元投稿がネストして入る（`fetchPost` が再帰的に解決する）:
```json
{
  "id": 99,
  "content": null,
  "is_repost": true,
  "original_post_id": 1,
  "author": { /* リポストしたユーザー */ },
  ...
}
```

#### Notification オブジェクト

```json
{
  "id": 1,
  "type": "like",
  "actor": { /* User オブジェクト */ },
  "post_id": 42,
  "post_excerpt": "今日もいい天気🌸",
  "parent_excerpt": null,
  "is_read": false
}
```

- `type` は `like` / `follow` / `repost` / `reply` / `footprint` のいずれか
- `post_id` は `like`・`repost`・`reply` のときに値を持つ。
- `post_excerpt` に対象投稿の本文（40文字で切り詰め）が入る。`reply` の場合、`post_id` は返信そのものを指すため、`parent_excerpt` に返信先（自分の投稿）の本文が入る。

#### Footprint オブジェクト

```json
{
  "visitor": { /* User オブジェクト */ },
  "visit_count": 3
}
```

並び順は最終訪問日時の降順だが、日時そのものはレスポンスに含まれない。

プロフィール訪問者を訪問者ごとに集計し、最終訪問日時の降順で返す。

---

### エンドポイント一覧

#### 認証

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| POST | `/register` | ユーザー登録 | 不要 |
| POST | `/login` | ログイン（セッション発行） | 不要 |
| POST | `/logout` | ログアウト（セッション破棄） | 要 |

##### ユーザー登録
- POST /register

`username` / `email` / `password` は必須。登録成功時はユーザー作成に加えてセッションも発行され、`session_id` クッキーが返る。

| 名前 | 型 | 備考 |
|---|---|---|
| username | string | 必須 |
| display_name | string | 任意 |
| email | string | 必須 |
| password | string | 必須 |

```json
// Request
{ "username": "aoi_haru", "display_name": "あおい", "email": "aoi@example.com", "password": "password123" }

// Response 201 + Set-Cookie: session_id=<token>; HttpOnly; Path=/
{ "user": { /* User オブジェクト */ } }
```

##### ログイン
- POST /login

メールアドレスとパスワードで認証し、成功時に24時間有効の `session_id` クッキーを発行する。

| 名前 | 型 | 備考 |
|---|---|---|
| email | string | 必須 |
| password | string | 必須 |

```json
// Request
{ "email": "aoi@example.com", "password": "password123" }

// Response 200 + Set-Cookie: session_id=<token>; HttpOnly; Path=/
{ "user": { /* User オブジェクト */ } }
```

##### ログアウト
- POST /logout

`session_id` を削除し、Cookie を期限切れにする。

```json
// Response 200
{ "message": "logged out" }
```

---

#### ユーザー

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| GET | `/me` | 自分のプロフィール取得 | 要 |
| GET | `/profile/:user_id` | プロフィール取得 | 不要 |
| PUT | `/profile` | プロフィール更新 | 要 |
| GET | `/users/:user_id/followers` | フォロワー一覧 | 不要 |
| GET | `/users/:user_id/following` | フォロー中一覧 | 不要 |
| POST | `/users/:user_id/follow` | フォローする | 要 |
| DELETE | `/users/:user_id/follow` | フォロー解除 | 要 |

##### 自分のプロフィール取得
- GET /me

ログイン中ユーザー自身の `User` を返す。

```json
// Response 200
{ "user": { /* User オブジェクト */ } }
```

##### プロフィールの取得
- GET /profile/:user_id

指定ユーザーのプロフィールを返す。認証済みで本人以外のプロフィールを閲覧した場合は、足跡記録と `footprint` 通知の生成が同時に走る。

| 名前 | 型 | 備考 |
|---|---|---|
| user_id | int64 | Path パラメータ |

```json
// Response 200
{ "user": { /* User オブジェクト */ } }
```

##### プロフィールの更新
- PUT /profile
  - `display_name` と `bio` を更新する。
  - 部分更新ではなく、リクエストにない/空のフィールドはそのまま上書きされる（`display_name` を省略すると空文字列になり、`bio` を省略すると `NULL` になる）。更新しない値も含めて毎回両方を送ること。

| 名前 | 型 | 備考 |
|---|---|---|
| display_name | string | 送信しない場合は空文字列で上書きされる |
| bio | string \| null | 送信しない場合は `NULL` で上書きされる |

```json
// Request
{ "display_name": "あおい", "bio": "春が好きです" }

// Response 200
{ "user": { /* User オブジェクト */ } }
```

##### フォロワー一覧
- GET /users/:user_id/followers

| 名前 | 型 | 備考 |
|---|---|---|
| user_id | int64 | Path パラメータ |

```json
// Response 200
{ "users": [ /* User オブジェクト の配列 */ ], "total": 120 }
```

##### フォロー中一覧
- GET /users/:user_id/following

| 名前 | 型 | 備考 |
|---|---|---|
| user_id | int64 | Path パラメータ |

```json
// Response 200
{ "users": [ /* User オブジェクト の配列 */ ], "total": 45 }
```

##### フォローする
- POST /users/:user_id/follow

対象ユーザーをフォローし、対象ユーザーへ `follow` 通知を作成する。自分自身はフォローできない。

| 名前 | 型 | 備考 |
|---|---|---|
| user_id | int64 | Path パラメータ（自分自身は不可） |

```json
// Response 200
{ "message": "followed" }
```

##### フォロー解除
- DELETE /users/:user_id/follow

| 名前 | 型 | 備考 |
|---|---|---|
| user_id | int64 | Path パラメータ |

```json
// Response 200
{ "message": "unfollowed" }
```

---

#### 投稿

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| GET | `/posts` | タイムライン取得 | 要 |
| POST | `/posts` | 新規投稿 | 要 |
| GET | `/posts/:id` | 投稿詳細取得 | 不要 |
| GET | `/users/:user_id/posts` | ユーザー投稿一覧（`type=posts`/`replies`） | 不要 |
| DELETE | `/posts/:id` | 投稿削除 | 要 |
| POST | `/replies` | 返信作成（body: `{post_id, content}`） | 要 |
| GET | `/posts/:id/thread` | スレッド取得（`{ancestors, post, replies}`） | 不要 |
| GET | `/posts/:id/thread/stream` | スレッドへの新規返信を SSE 配信 | 不要 |

##### 返信作成
- POST /replies

返信も `posts` テーブルの1行として保存される（`parent_post_id` に返信先IDが入る）。
返信先の投稿者に `reply` 型の通知が作られる（**直接の返信先の著者のみ**。スレッド参加者全員には送らない）。

```json
// リクエスト
{ "post_id": 1, "content": "たしかにそうですね" }

// レスポンス 201
{ "post": { /* Post オブジェクト */ } }
```

存在しない `post_id` を指定すると 404、本文が空または141文字以上なら 400。

##### スレッド取得
- GET /posts/:id/thread

`:id` を含む会話全体を返す。`ancestors` は `:id` の返信ではなく、親を辿った**祖先チェーン（古い順）**。
`replies` は Post の配列ではなく、`{post, replies}` を**再帰的に入れ子にしたツリー**である点に注意。

```json
{
  "ancestors": [ /* Post オブジェクトの配列（古い順） */ ],
  "post": { /* :id 自身 */ },
  "replies": [
    {
      "post": { /* 返信 */ },
      "replies": [ /* 同じ形が再帰的に続く */ ]
    }
  ]
}
```

タイムライン（`GET /posts`）は3フィードとも**返信を除外**する（返信はスレッド画面でのみ表示）。

##### タイムライン取得
- GET /posts

`feed` クエリパラメータでタイムラインの種類を切り替えられる。`page` / `per_page` に対応。

| 名前 | 型 | 備考 |
|---|---|---|
| feed | string | Query パラメータ。省略時 `following` |
| page | int | Query パラメータ。省略時 1 |
| per_page | int | Query パラメータ。省略時 20。**50 を超える値を指定すると 50 ではなく 20 になる** |

`feed` の値ごとの挙動:

| 値 | 説明 |
|---|---|
| `following`（省略時） | フォロー中ユーザーの投稿を `created_at DESC, id DESC` で返す |
| `latest` | 全ユーザーの投稿を新着順（`created_at DESC, id DESC`）で返す |
| `recommended` | 直近24時間のいいね数が多い順に返す（同数の場合は `created_at DESC, id DESC`） |

```
GET /posts?feed=recommended&page=1&per_page=20
```
```json
// Response 200
{
  "posts": [ /* Post オブジェクト の配列 */ ],
  "total": 5432,
  "page": 1,
  "per_page": 20
}
```

##### 新規投稿
- POST /posts

`content` は 1 文字以上 140 文字以下（rune 数）である必要がある。

| 名前 | 型 | 備考 |
|---|---|---|
| content | string | 必須。1-140 文字 |

```json
// Request
{ "content": "今日もいい天気🌸" }  // 最大140文字

// Response 201
{ "post": { /* Post オブジェクト */ } }
```

##### 投稿詳細取得
- GET /posts/:id

| 名前 | 型 | 備考 |
|---|---|---|
| id | int64 | Path パラメータ |

```json
// Response 200
{ "post": { /* Post オブジェクト */ } }
```

##### ユーザー投稿一覧
- GET /users/:user_id/posts

指定ユーザーの投稿一覧を返す。`page` / `per_page` に対応。

| 名前 | 型 | 備考 |
|---|---|---|
| user_id | int64 | Path パラメータ |
| page | int | Query パラメータ。省略時 1 |
| per_page | int | Query パラメータ。省略時 20。**50 を超える値を指定すると 50 ではなく 20 になる** |

```json
// Response 200
{ "posts": [ /* Post オブジェクト の配列 */ ], "total": 300, "page": 1, "per_page": 20 }
```

##### 投稿削除
- DELETE /posts/:id

自分の投稿のみ削除できる。

| 名前 | 型 | 備考 |
|---|---|---|
| id | int64 | Path パラメータ |

```json
// Response 200
{ "message": "deleted" }
```

---

#### いいね

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| GET | `/posts/:id/likes` | いいね一覧 | 不要 |
| POST | `/likes` | いいねする | 要 |
| DELETE | `/likes/:post_id` | いいね解除 | 要 |

##### いいね一覧
- GET /posts/:id/likes

| 名前 | 型 | 備考 |
|---|---|---|
| id | int64 | Path パラメータ |

```json
// Response 200
{ "users": [ /* User オブジェクト の配列 */ ] }
```

##### いいねする
- POST /likes

いいね登録後、投稿作成者に `like` 通知を作成する。すでにいいね済みでも重複登録はされない。

| 名前 | 型 | 備考 |
|---|---|---|
| post_id | int64 | 必須 |

```json
// Request
{ "post_id": 1 }

// Response 200
{ "likes_count": 43 }
```

##### いいね解除
- DELETE /likes/:post_id

| 名前 | 型 | 備考 |
|---|---|---|
| post_id | int64 | Path パラメータ |

```json
// Response 200
{ "likes_count": 42 }
```

---

#### リポスト

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| POST | `/reposts` | リポストする | 要 |
| DELETE | `/reposts/:post_id` | リポスト取り消し | 要 |

##### リポストする
- POST /reposts

`reposts` テーブルへ登録し、同時に `posts` テーブルへ `is_repost=true` の投稿行を作る。元投稿の作成者に `repost` 通知を作成する。

| 名前 | 型 | 備考 |
|---|---|---|
| post_id | int64 | 必須 |

```json
// Request
{ "post_id": 1 }

// Response 200
{ "reposts_count": 9 }
```

##### リポスト取り消し
- DELETE /reposts/:post_id

`reposts` と、対応するリポスト投稿行を削除する。

| 名前 | 型 | 備考 |
|---|---|---|
| post_id | int64 | Path パラメータ |

```json
// Response 200
{ "reposts_count": 8 }
```

---

#### 検索

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| GET | `/search` | 投稿・ユーザー検索 | 不要 |

`q` は必須。`type` は `posts`（省略時デフォルト）または `users`。`page` / `per_page` に対応。

| 名前 | 型 | 備考 |
|---|---|---|
| q | string | Query パラメータ。必須 |
| type | string | Query パラメータ。`posts` または `users`（省略時 `posts`） |
| page | int | Query パラメータ。省略時 1 |
| per_page | int | Query パラメータ。省略時 20。**50 を超える値を指定すると 50 ではなく 20 になる** |

```
GET /search?q=桜&type=posts&page=1&per_page=20
GET /search?q=aoi&type=users&page=1&per_page=20
```
```json
// Response 200 (type=posts)
{ "posts": [...], "total": 42, "page": 1, "per_page": 20 }

// Response 200 (type=users)
{ "users": [...], "total": 5, "page": 1, "per_page": 20 }
```

---

#### 通知

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| GET | `/notifications` | 通知一覧 | 要 |
| POST | `/notifications/read` | 未読通知を既読化 | 要 |
| GET | `/notifications/unread_count` | 未読件数取得 | 要 |
| GET | `/notifications/stream` | 通知を SSE 配信 | 要 |

`follow`・`like`・`repost`・`reply`・`footprint`（プロフィール訪問）の各アクションで自動的に生成される。

`GET /notifications` は `type` で種別を絞り込める（`all`（既定）/`reply`/`like`/`repost`/`follow`/`footprint`）。
`total` は絞り込み後の件数だが、`unread_count` はバッジ用のため常に全種別の未読数を返す。

##### 通知一覧
- GET /notifications

現在実装では `Notification` のうち `created_at` はレスポンスに含めていない。

| 名前 | 型 | 備考 |
|---|---|---|
| page | int | Query パラメータ。省略時 1 |
| per_page | int | Query パラメータ。省略時 20。**50 を超える値を指定すると 50 ではなく 20 になる** |
| type | string | Query パラメータ。省略時 `all`。`reply` / `like` / `repost` / `follow` / `footprint` で種別を絞る |

`total` は絞り込み後の件数だが、`unread_count` は常に全種別の未読数を返す（バッジ表示用）。

```json
// GET /notifications
{
  "notifications": [ /* Notification オブジェクト の配列 */ ],
  "total": 42,
  "unread_count": 5,
  "page": 1,
  "per_page": 20
}
```

##### 未読通知を既読化
- POST /notifications/read

```json
// Response 200
{ "message": "ok" }
```

##### 未読件数取得
- GET /notifications/unread_count

```json
// Response 200
{ "unread_count": 5 }
```

---

#### 足跡

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| GET | `/me/footprints` | 自分のプロフィール訪問者一覧 | 要 |

`GET /profile/:user_id` の呼び出し時（認証済みかつ本人以外の閲覧）に自動記録される。

##### プロフィール訪問者一覧
- GET /me/footprints

ログイン中ユーザー自身の訪問者一覧のみを返す（`user_id` を指定して他人の訪問者一覧を見ることはできない）。
現在実装では `Footprint` のうち `last_visited` はレスポンスに含めていない。

| 名前 | 型 | 備考 |
|---|---|---|
| page | int | Query パラメータ。省略時 1 |
| per_page | int | Query パラメータ。省略時 20。**50 を超える値を指定すると 50 ではなく 20 になる** |

```json
// Response 200
{
  "footprints": [ /* Footprint オブジェクト の配列 */ ],
  "total": 30,
  "page": 1,
  "per_page": 20
}
```

---

#### トレンド

| Method | Path | 説明 | 認証 |
|---|---|---|---|
| GET | `/trending` | 直近1時間のいいね数が多い投稿トップ20 | 不要 |

認証は任意。ログイン中なら `liked_by_me` / `reposted_by_me` が反映された `Post` が返る。

> **シードデータでの注意**: 集計対象が「直近1時間のいいね」なのに対し、シードスクリプトは
> `likes.created_at` をすべて実行時刻で作る。そのため**シード投入から1時間が過ぎると結果が空になる**。
> 翌日に計測して「壊れている」と誤解しないこと（シードを投入し直せば戻る）。
> おすすめフィード（`feed=recommended`、直近24時間のいいね数順）も同様に、24時間後には
> 実質的に時系列順へ退化する。

```json
// Response 200
{
  "trending": [
    { "post": { /* Post オブジェクト */ }, "recent_likes": 87 },
    ...
  ]
}
```
