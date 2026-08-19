# 踏み台用 SSH 鍵

チーム共用の踏み台アクセス用キーペアをここに置く（**公開鍵のみコミット**、秘密鍵は 1Password で共有）。

```bash
# 生成（秘密鍵はコミットしないこと）
ssh-keygen -t ed25519 -C "team-e-bastion" -f bastion
mv bastion.pub ./bastion.pub   # ← これだけコミット（variables.tf の既定パス）
# bastion（秘密鍵）は 1Password へ登録し、ローカルの ~/.ssh/ に配置して使う
# CD の migration ジョブ用には、秘密鍵を GitHub Secrets の BASTION_SSH_KEY に登録する
```
