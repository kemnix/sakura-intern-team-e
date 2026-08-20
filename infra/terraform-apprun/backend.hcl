# tfstate の保存先(バケット名はサイト内でユニークのため、配布先はこのファイルだけ書き換える)。
# terraform init 時に必ず渡す:  terraform init -backend-config=backend.hcl
# ここを変更したら既存環境では state の移送も必要(DEPLOY.md 付録B参照)。
# deploy-apps.sh もこのファイルから接続情報を読むため、書式(key = "value")を崩さないこと。
bucket = "sakuravel-app-tf"
region = "jp-north-1" # isk01(石狩第1サイト)。東京(tky01)は jp-east-1
endpoints = {
  s3 = "https://s3.isk01.sakurastorage.jp"
}
