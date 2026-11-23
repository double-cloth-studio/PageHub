# 单配置部署与统一运维

## 首次安装

部署目标为支持 Docker Compose v2、systemd 和 Git 的 Ubuntu/Debian，安装预检沿用 Python 3.11+、curl、flock 等依赖检查。缺少 Docker 时可显式设置 `PAGEHUB_INSTALL_DOCKER=true`。

```bash
cp config/pagehub.yaml.example pagehub.yaml
# 修改公开 HTTPS Origin、应用入口和固定 Compose 项目名
sudo bash install.sh --config ./pagehub.yaml
```

安装时打印计划，输入 JSON 最外层 `sha256` 确认。日记初始化通过其原生终端工具设置密码。已有认证文件、PageHub Token 和 runtime 数据卷会被复用。配置安装到 `/etc/pagehub/pagehub.yaml`，目录权限为 0700，文件为 root:root 0600。后续只需维护这个文件。

`platform.public_origin` 派生 `SITE_DOMAIN`、`SITE_SCHEME` 和 `TRAVEL_PUBLIC_ORIGIN`。日记 Portal 运行时要求 HTTPS Origin，`TRAVEL_BASE_PATH` 去掉尾部斜杠，网关剥离一次应用前缀。配置中允许的仓库和 Compose 能力由 Go 校验器限制，Schema 位于 `config/schema/pagehub.schema.json`。日记的 Origin 约束来自其运行时配置校验。[Travel Diary 配置实现](https://github.com/double-cloth-studio/travel-diary/blob/main/js/deployment-config.js)

`settings` 的值使用字符串。密码、Token 和隧道凭据通过独立 Secret 或原生交互维护。`TRAVEL_WRITE_MODE` 默认为 `local`，启用 `remote` 时要求公开 HTTPS 网关通过证书与健康验证，应用认证初始化也须成功。

## 计划与配置应用

```bash
sudo pagehub plan
sudo pagehub apply
sudo pagehub apply --approved-plan <本次计划最外层sha256>
sudo pagehub status
sudo pagehub doctor
```

`plan` 读取配置、登记及现有容器，查询目标分支 SHA。它不创建源码缓存、容器、卷或路由。首次安装冻结目标 SHA，普通 `apply` 复用已安装 SHA，配置变化单独计算摘要。`update` 查询并冻结远程分支的新 SHA。

计划绑定配置、当前登记指纹、目标 commit，以及接管工程的真实容器摘要。执行前再次检查状态，过期计划返回错误。新增和更新按静态应用、Compose 应用的顺序执行，逐项提交，失败停止后续操作。应用配置中的 ID、驱动、仓库和 Compose 项目名承担稳定身份。变更这些字段及已安装平台的 root、Origin、监听端口需要独立迁移。

派生文件位于安装根目录的 `config/pagehub.env`、`config/pagehub.yaml`、应用发行目录和 `state/`。`config/pagehub.yaml` 保存已应用的配置快照，`state/desired-config.sha256` 保存摘要。正式安装的 CLI 默认读取 `/etc/pagehub/pagehub.yaml`。

## 运行管理

```bash
sudo pagehub start travel-diary
sudo pagehub stop travel-diary
sudo pagehub restart travel-diary
sudo pagehub logs travel-diary
sudo pagehub logs gateway
```

日记使用配置中固定的 Compose 项目名和 `<project>_runtime` 卷。托管运行配置只包含 app 服务，内部端口 9000 加入 `pagehub-ingress`。Compose 的构建上下文、字段、挂载和权限经过白名单检查，发行配置冻结到实际镜像 ID。原项目独立网关继续由其部署工具管理。

静态应用的 stop/start 禁用和恢复公开路由，停止后入口返回 503。`status` 输出应用 SHA、镜像或资产摘要、真实容器运行状态、HTTP 健康及路由状态。日记的 `manual` 启动策略保留显式启动语义。静态应用使用 `always`。

经审批并完成部署的托管应用可以进入主页目录。管理员保存过展示布局时，可在“编辑展示”中添加新应用。离线验收记录继续保存真实验收结果及对应指纹。

应用条目支持 `description` 和 `icon_path`。图标使用门户 `/assets/` 下的同源图片。已有部署可将配置示例中的对应字段补入 `/etc/pagehub/pagehub.yaml`，再执行 `pagehub apply`。门户的展示配置可覆盖这两项。

## 更新、备份与恢复

```bash
sudo pagehub update --check
sudo pagehub update travel-diary --check
sudo pagehub update travel-diary
sudo pagehub update --all
sudo pagehub update self
sudo pagehub backup travel-diary
sudo pagehub backup verify travel-diary /srv/pagehub/backups/travel-diary/<备份目录>
sudo pagehub restore travel-diary /srv/pagehub/backups/travel-diary/<备份目录>
sudo pagehub rollback travel-diary
sudo pagehub rollback self
```

应用版本在隔离目录准备。日记升级使用旧镜像停写、原生备份和原生校验，再切换候选镜像并验证共享网关入口。成功后记录 commit、实际镜像 ID、备份收据和前一发行版本。失败时尝试用升级前备份恢复数据和原镜像，恢复失败则保留维护状态并返回错误。

日记备份包含业务数据、密码和回收站，收据保存实际卷名、镜像、SHA 和校验结果。独立备份完成后恢复原运行状态，升级使用的备份保持停写直至版本切换。显式恢复需要应用 ID 与 `RESTORE` 两次确认，恢复前再备份当前数据。日记的 `restore --dry-run` 执行原生备份完整性验证。静态应用备份沿用归档校验与隔离恢复演练。

回滚保留当前数据的备份，然后切换前一发行版本。涉及数据格式变化时，管理员应核对备份恢复记录。`retain_releases` 控制应用历史产物和已通过健康验收的平台历史发行版本，当前版本及回滚引用始终保留。

`update --all` 先准备并构建目标平台，验证配置与登记兼容性，再更新应用，最后通过独立 `pagehub-updater.service` 切换平台。CLI 等待独立任务的匹配收据。控制平面重启期间，更新事务继续运行。平台配置、凭据和前一版本引用在切换前备份，健康失败触发旧版本恢复。

```bash
sudo pagehub logs updater
sudo cat /srv/pagehub/state/self-result.json
```

运维记录位于 `state/operations/`，管理台的“任务与审计”汇总 Runner 与 Manager 记录。记录包含计划摘要、旧新 SHA、已完成组件、实际镜像/资产摘要、备份路径和结果。中断任务保留已提交组件，重新执行前可先运行 `status` 和 `plan`。

## 迁移与卸载

```bash
sudo pagehub plan --adopt
sudo pagehub apply --adopt
sudo pagehub uninstall travel-diary
sudo pagehub uninstall travel-diary --purge-data --backup /srv/pagehub/backups/travel-diary/<备份目录>
```

接管日记前先核对原项目名和真实卷。当前托管适配器接管单个 app 容器、固定 runtime 卷、无宿主端口和额外权限的 Portal 工程。计划展示原镜像和卷名，审批绑定实际容器配置。原版本源码未知时明确记录 `source_unknown`，首次托管升级后记录新 SHA。现有包格式和 external/external-compose 接入仍可使用相应命令。

应用卸载移除容器、路由与登记，并将声明式条目的 `enabled` 改为 false。默认保留日记卷、源码和备份，静态应用清理自身发行文件。永久删除要求先验证备份，再输入应用 ID 和实际卷名。Web 管理台提供保留数据的应用卸载操作。

```bash
sudo pagehub uninstall self --dry-run
sudo pagehub uninstall self
# 独立入口，可在 CLI 被移除后执行，也支持传入 --yes
sudo bash uninstall.sh
```

平台卸载停止并移除 PageHub 的 systemd 单元、命令入口、网关和门户容器。应用容器、Docker 网络、持久卷、配置、备份和发行目录保留。共享网关入口会停止服务。重新执行 `install.sh` 可恢复平台。

## Web 管理

```bash
sudo pagehub admin set-password
ssh -L 8091:127.0.0.1:8091 user@server
```

访问 `/admin/`。托管应用展示 SHA 和驱动，支持启停、更新、备份、恢复、回滚与卸载。更新表单显示当前与目标 commit。维护操作要求重新输入管理员密码和目标 ID，任务通过 Manager 转发到受限 Runner。

“更新平台”使用 `self` 作为确认 ID，展示平台当前与目标 commit。独立 updater 按请求 ID 保存收据，Manager 重启后重新登录并打开“任务与审计”，可继续查询同一次更新结果。API 使用 `/admin/api/apps/self/plan` 和 `/admin/api/apps/self/update-self`。

API 的 `GET /admin/api/apps/<id>/plan` 返回该目标的计划，`POST /admin/api/apps/<id>/update` 使用返回的 `sha256` 作为 `approved_plan_sha256`。维护请求还包含 `password`、`confirm_id`，恢复请求另含 `backup`。返回的任务 ID 通过 `GET /admin/api/jobs/<id>` 查询，备份结果位于 `result.backup`。凭据仅在请求和页面内存中使用。

## 验收范围

自动化覆盖严格字段校验、重复 ID/路径、Origin、秘密字段拒绝、幂等计划、过期审批、重认证和独立更新收据。Linux 容器测试覆盖真实 Runner、Docker、Caddy 和 HTTP 路由，源码获取使用隔离 Git 夹具以固定测试版本。

`tests/integration/run.sh` 运行原有适配器与接口回归及托管静态流程。设置 `PAGEHUB_TEST_DIARY_IMAGE` 后还使用指定真实日记镜像运行原生备份恢复与生命周期测试，`--managed-only` 可单独运行托管流程。测试需要独立 Docker Engine，并默认保留诊断卷。

在按开发说明准备的隔离测试容器内执行。

```bash
PAGEHUB_TEST_DIARY_IMAGE=travel-diary:verified bash /workspace/tests/integration/run.sh --managed-only
```

本次浏览器检查使用 `http://127.0.0.1:15175/admin/` 的演示接口，覆盖 1440×1000 与 390×844、更新计划、错误 ID 禁用确认、正确确认和完成反馈。Browser 插件未提供，使用 Playwright CLI。页面身份、非空内容、框架覆盖层、控制台和操作反馈均已检查。

2026-11-17 本地回归通过 Windows Go 单测与 vet、Linux Go 单测、前端单测与生产构建、原有完整 Docker 集成流程及托管应用流程。日记测试使用真实 `travel-diary:pagehub-final` 镜像，其源码 revision 为 `e1297c9019a585e6c9642446fc55accbc1fa8e6a`，覆盖原生备份校验恢复、更新失败后的数据与镜像恢复、已有项目接管及双确认清理。真实 GitHub 源码冻结测试解析到 Whalgebra commit `a95955610e456ace18e7564cf7fd0843872ebe56`。前端构建仍有既有大 chunk 提示。

真实 Ubuntu/Debian VM 冷安装、整机 reboot、ARM64、真实 v4 部署现场和 SakuraFrp HTTPS/Cookie 验收仍需在目标环境执行。独立 updater 的 systemd 进程重启与失败回退也需要 VM 端到端记录。
