# PageHub

## 单配置部署与统一运维

```bash
cp config/pagehub.yaml.example pagehub.yaml
# 修改配置中的公开 HTTPS Origin 与应用路径
sudo bash install.sh --config ./pagehub.yaml
sudo pagehub status
sudo pagehub update --check
sudo pagehub update --all
```

安装后维护 `/etc/pagehub/pagehub.yaml`，通过 `pagehub plan/apply` 应用变更。支持 Git SHA 冻结、日记与静态站点托管、原生备份恢复、版本回滚、保留数据卸载和独立平台自更新。完整命令、迁移范围与验收状态见[统一部署说明](docs/UNIFIED_DEPLOYMENT.md)。

## 部署

```bash
# 安装 PageHub，并设置对外访问地址
sudo PAGEHUB_PUBLIC_ORIGIN=https://your-domain.example:63445 bash install.sh
```

```bash
# 安装 PageHub，缺少 Docker 时自动安装
sudo PAGEHUB_INSTALL_DOCKER=true bash install.sh
```

```bash
# 启动或恢复平台服务
sudo bash /srv/pagehub/current/restart.sh
# 检查平台服务的健康状态
sudo bash /srv/pagehub/current/restart.sh --check
```

## 状态、诊断与日志

```bash
# 建立 SSH 隧道，访问服务器上的管理入口
ssh -L 8091:127.0.0.1:8091 user@server
# 查看平台与应用状态
sudo bash /srv/pagehub/current/pagehub.sh status
# 执行环境与配置诊断
sudo bash /srv/pagehub/current/pagehub.sh doctor
# 查看网关日志
sudo bash /srv/pagehub/current/pagehub.sh logs gateway
# 查看门户日志
sudo bash /srv/pagehub/current/pagehub.sh logs portal
# 查看管理服务日志
sudo bash /srv/pagehub/current/pagehub.sh logs manager
# 查看运维执行服务日志
sudo bash /srv/pagehub/current/pagehub.sh logs runner
# 查看指定应用的日志
sudo bash /srv/pagehub/current/pagehub.sh logs my-app
```

## 导入应用

```bash
# 输出已有 Compose 项目的配置摘要与逐项授权候选
sudo bash pagehub.sh compose-review /srv/existing compose.yaml existing
# 预检应用包，查看导入计划与包摘要
sudo bash pagehub.sh add-app /srv/uploads/my-app.zip --dry-run
# 交互确认审核结果后导入应用
sudo bash pagehub.sh add-app /srv/uploads/my-app.zip
```

```bash
# 使用已审核的精确包摘要执行非交互导入
sudo bash pagehub.sh add-app /srv/uploads/my-app.zip --approved-sha256 <审核后的64位摘要>
# 预检已有 Compose 项目的接管计划
sudo bash pagehub.sh adopt-app /srv/uploads/my-app.zip --project-name existing-project --dry-run
# 按指定项目名接管已有 Compose 项目
sudo bash pagehub.sh adopt-app /srv/uploads/my-app.zip --project-name existing-project
```

## 生命周期与卸载

```bash
# 启动指定应用
sudo bash pagehub.sh app start my-app
# 停止指定应用
sudo bash pagehub.sh app stop my-app
# 重启指定应用
sudo bash pagehub.sh app restart my-app
# 移除应用登记与路由，外部接入项目保留原部署
sudo bash pagehub.sh remove-app my-app
# 重新加载共享网关配置
sudo bash pagehub.sh gateway reload
# 预览平台卸载范围，确认后移除 PageHub 服务及命令入口
sudo pagehub uninstall self --dry-run
sudo pagehub uninstall self
```

## 备份、恢复与升级

```bash
# 备份应用文件或具名卷，输出备份目录
sudo bash pagehub.sh backup my-app
# 校验备份并在隔离目录或卷中执行恢复演练
sudo bash pagehub.sh backup verify my-app /srv/pagehub/backups/my-app/<timestamp>
# 演练恢复并生成验证报告
sudo bash pagehub.sh restore my-app /srv/pagehub/backups/my-app/<timestamp> --dry-run
# 卷恢复前停止应用
sudo bash pagehub.sh app stop my-app
# 自动备份当前数据后，恢复指定备份
sudo bash pagehub.sh restore my-app /srv/pagehub/backups/my-app/<timestamp>
# 恢复完成后启动应用
sudo bash pagehub.sh app start my-app
# 使用维护收据升级应用，卷应用需先验证对应备份
sudo bash pagehub.sh update my-app /srv/uploads/my-app-v2.zip --maintenance-receipt /srv/approved/maintenance.json
# 将静态应用回滚到上一版本
sudo bash pagehub.sh rollback static-demo
# 使用维护收据回滚卷应用，需先验证对应备份
sudo bash pagehub.sh rollback my-app --maintenance-receipt /srv/approved/rollback.json
# 从指定发布目录升级 PageHub 平台
sudo bash pagehub.sh platform-update /srv/uploads/pagehub-new
# 将 PageHub 平台回滚到上一版本
sudo bash pagehub.sh platform-rollback
```

## 本地开发与检查

```bash
# 以下前端命令在 frontend 目录执行
cd frontend
# 按锁文件安装前端依赖
npm ci
# 使用演示数据启动独立 UI 开发服务
npm run dev:ui
# 启动对接真实后端的前端开发服务
npm run dev
# 运行前端测试
npm test
# 构建前端生产产物
npm run build
```

```bash
# 以下后端命令在项目根目录下进入 backend 后执行
cd backend
# 运行全部 Go 测试
go test ./...
# 执行 Go 静态检查
go vet ./...
# 启动管理服务进行本地联调
go run ./cmd/manager
```

## 离线打包

```bash
# 以下命令在项目根目录执行，生成当前架构的源码安装包
bash scripts/package-release.sh source
# 打包已构建的二进制、前端产物及本地 Docker 镜像
bash scripts/package-release.sh offline
# 核验下载的离线包 SHA-256，以下以 amd64 为例
sha256sum -c pagehub-0.1.1-offline-amd64.tar.gz.sha256
# 创建安装包解压目录
mkdir pagehub-install
# 解压离线安装包
tar -xzf pagehub-0.1.1-offline-amd64.tar.gz -C pagehub-install
# 在已具备运行依赖的目标机上安装离线包
sudo bash pagehub-install/install.sh
```
