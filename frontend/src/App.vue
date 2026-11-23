<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { gsap } from "gsap";
import SnakeWorld from "./features/snake/SnakeWorld.vue";
import ProjectUniverse from "./features/projects/ProjectUniverse.vue";
import { searchProjects } from "./features/projects/project-search";
import ProjectManager from "./features/admin/ProjectManager.vue";
import MaintenancePanel from "./features/admin/MaintenancePanel.vue";
import type { MaintenanceAction } from "./models/app";
import DanmakuPanel, { type Message } from "./features/social/DanmakuPanel.vue";
import Icon from "./ui/Icon.vue";
import { defaultPresentation, type Presentation } from "./models/presentation";
import { stateLabels } from "./models/app";
import type { Direction } from "./features/snake/engine";
import AppCatalog from "./features/app-catalog/AppCatalog.vue";
import { loadApps, loadStatus, request, runAction } from "./api/client";
import type { Action, AppEntry, Job } from "./models/app";
import { actionLabels } from "./models/app";

type Mode =
  | "HOME_AUTO"
  | "TRANSITION_TO_PROJECTS"
  | "PROJECTS_VIEW"
  | "TRANSITION_FROM_PROJECTS"
  | "TRANSITION_TO_HOME"
  | "GAME_RUNNING"
  | "GAME_PAUSED"
  | "GAME_DEAD"
  | "GAME_WON"
  | "ADMIN_AUTH_ENTRY"
  | "ADMIN_PROJECTS";
const mode = ref<Mode>("HOME_AUTO");
const panel = ref<"status" | "social" | "">("");
const settings = ref<Presentation>(structuredClone(defaultPresentation));
const world = ref<InstanceType<typeof SnakeWorld>>();
const universe = ref<InstanceType<typeof ProjectUniverse>>();
const hero = ref<HTMLElement>();
const flights = ref<HTMLElement>();
const dock = ref<HTMLElement>();
const dockHeight = ref(80);
let dockObserver: ResizeObserver;
const editor = ref(false);
const audit = ref<(Job & { created_at?: string; actor?: string })[]>([]);
const showAudit = ref(false);
const maintenance = ref<{ app: AppEntry; action: MaintenanceAction }>();
const loginBusy = ref(false);
const loginMethod = ref("password");
const showCatalog = ref(false);
const score = ref(0);
const speed = ref(8);
const desktop = ref(false);
const sources = ref<
  { x: number; y: number; width: number; height: number; color: string }[]
>([]);
const isGame = computed(() => mode.value.startsWith("GAME_"));
const inProjects = computed(
  () =>
    mode.value === "PROJECTS_VIEW" ||
    mode.value === "TRANSITION_TO_PROJECTS" ||
    mode.value === "TRANSITION_FROM_PROJECTS",
);
const visibleApps = computed(() => {
  const byId = new Map(apps.value.map((app) => [app.id, app]));
  if (!settings.value.revision)
    return apps.value.filter((app) => app.displayReady ?? app.offlineReady);
  return [...settings.value.projects]
    .sort((a, b) => a.displayOrder - b.displayOrder)
    .filter((project) => project.displayEnabled)
    .flatMap((project) => {
      const app = byId.get(project.appId);
      return (app?.displayReady ?? app?.offlineReady)
        ? [
            {
              ...app,
              name: project.titleOverride || app.name,
              description: project.descriptionOverride || app.description,
              iconPath: project.iconPath || app.iconPath,
              keywords: project.keywords,
            },
          ]
        : [];
    });
});
let transition: gsap.core.Timeline | undefined;
let clicks: number[] = [];
let motion: gsap.MatchMedia;
const adminMode = /^\/admin\/?$/.test(window.location.pathname);
const apps = ref<AppEntry[]>([]);
const tokenInput = ref("");
const token = ref("");
const query = ref("");
const kind = ref("all");
const loading = ref(false);
const notice = ref("");
const error = ref("");
const snapshot = ref(false);
const busy = ref<Record<string, boolean>>({});
const lastUpdated = ref("");
let timer: ReturnType<typeof setInterval> | undefined;
let disposed = false;
let generation = 0;

const filtered = computed(() =>
  searchProjects(
    apps.value.filter((app) => kind.value === "all" || kind.value === app.kind),
    query.value,
  ),
);
const healthy = computed(
  () => visibleApps.value.filter((app) => app.state === "healthy").length,
);

async function refresh(force = false) {
  if (loading.value && !force) return;
  loading.value = true;
  error.value = "";
  const current = ++generation;
  try {
    let entries: AppEntry[];
    let fromSnapshot = false;
    try {
      entries = await loadApps(token.value);
    } catch (cause) {
      if (token.value) throw cause;
      entries = await request<AppEntry[]>("/_pagehub/catalog.json");
      fromSnapshot = true;
    }
    if (disposed || generation !== current) return;
    snapshot.value = fromSnapshot;
    apps.value = entries.map((app) => ({ ...app, state: "unknown" }));
    if (!snapshot.value) {
      let cursor = 0;
      await Promise.allSettled(
        Array.from({ length: Math.min(4, apps.value.length) }, async () => {
          while (
            cursor < entries.length &&
            generation === current &&
            !disposed
          ) {
            const app = apps.value[cursor++];
            if (!app) break;
            try {
              const status = await loadStatus(app.id, token.value);
              if (generation === current && !disposed) app.state = status.state;
            } catch {
              app.state = "unknown";
            }
          }
        }),
      );
    }
    if (generation === current && !disposed)
      lastUpdated.value = new Date().toLocaleTimeString("zh-CN", {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      });
  } catch (cause) {
    if (generation === current && !disposed)
      error.value = cause instanceof Error ? cause.message : "应用清单加载失败";
  } finally {
    if (generation === current) loading.value = false;
  }
}

async function login() {
  const credential = tokenInput.value.trim();
  if (!credential) return;
  error.value = "";
  loginBusy.value = true;
  try {
    if (loginMethod.value === "password") {
      const session = await request<{ csrf: string }>(
        "/admin/api/session",
        "",
        "POST",
        undefined,
        { password: credential },
      );
      token.value = `session:${session.csrf}`;
    } else {
      await loadApps(credential);
      token.value = credential;
    }
    settings.value = await request<Presentation>(
      "/admin/api/portal",
      token.value,
    );
    tokenInput.value = "";
    mode.value = "ADMIN_PROJECTS";
    await refresh(true);
    if (showAudit.value) await refreshAudit();
  } catch (cause) {
    token.value = "";
    error.value = cause instanceof Error ? cause.message : "连接失败";
  } finally {
    loginBusy.value = false;
  }
}

async function logout() {
  try {
    await request("/admin/api/session/logout", token.value, "POST");
  } catch {
    /* 页面退出仍清除内存凭据。 */
  }
  token.value = "";
  tokenInput.value = "";
  mode.value = "ADMIN_AUTH_ENTRY";
  editor.value = false;
  maintenance.value = undefined;
  void refresh(true);
}

async function action(app: AppEntry, operation: Action, approval?: unknown) {
  if (
    !approval &&
    operation !== "start" &&
    !window.confirm(
      `确认${actionLabels[operation]} ${app.name}？当前连接可能中断。`,
    )
  )
    return;
  busy.value[app.id] = true;
  error.value = "";
  notice.value = "";
  let job: Job | undefined;
  if (operation === "update-self") showAudit.value = true;
  try {
    job = await runAction(app.id, operation, token.value, approval);
    if (operation === "update-self") await refreshAudit();
    if (operation === "stop") app.state = "stopping";
    if (operation === "start" || operation === "restart")
      app.state = "starting";
    const deadline = Date.now() + (approval ? 31 * 60_000 : 210_000);
    while (job.state === "queued" || job.state === "running") {
      if (disposed) return;
      if (Date.now() > deadline)
        throw new Error("任务等待超时，请查看任务记录和实际运行状态");
      await new Promise((resolve) => setTimeout(resolve, 1000));
      job = await request<Job>(`/admin/api/jobs/${job.id}`, token.value);
      notice.value = `${app.name} · ${job.state === "queued" ? "任务已排队" : job.state === "running" ? "任务执行中" : "正在确认运行状态"}`;
    }
    if (job.state === "error") throw new Error(job.error || "任务执行失败");
    notice.value = `${app.name} ${actionLabels[operation]}任务已完成`;
    if (job.result?.backup)
      notice.value += ` · 已验证备份 ${job.result.backup}`;
    await refresh();
  } catch (cause) {
    if (
      operation === "update-self" &&
      job?.result?.request_id &&
      job.state !== "error"
    ) {
      error.value = "";
      token.value = "";
      tokenInput.value = "";
      mode.value = "ADMIN_AUTH_ENTRY";
      notice.value =
        "平台更新已提交，管理服务重启后请重新登录并查看任务与审计。";
      return;
    }
    await refresh();
    error.value = cause instanceof Error ? cause.message : "操作失败";
  } finally {
    delete busy.value[app.id];
  }
}

function submitMaintenance(approval: unknown) {
  if (!maintenance.value) return;
  const { app, action: operation } = maintenance.value;
  maintenance.value = undefined;
  void action(app, operation, approval);
}

function beginMaintenance(app: AppEntry, operation: MaintenanceAction) {
  notice.value = "";
  error.value = "";
  maintenance.value = { app, action: operation };
}

function beginPlatformUpdate() {
  beginMaintenance(
    {
      id: "self",
      name: "PageHub",
      path: "/",
      kind: "compose",
      startup: "always",
      state: "unknown",
      actions: [],
      managed: true,
    },
    "update-self",
  );
}

async function loadAudit() {
  showAudit.value = !showAudit.value;
  if (!showAudit.value) return;
  await refreshAudit();
}
async function refreshAudit() {
  try {
    audit.value = await request<Job[]>("/admin/api/audit", token.value);
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "审计记录读取失败";
  }
}
async function loadPresentation() {
  try {
    settings.value = await request<Presentation>("/_pagehub/api/portal");
  } catch {
    /* 保留编译内资料，目录接口独立可用。 */
  }
}
function avatarClick() {
  const now = Date.now();
  clicks = [...clicks.filter((time) => now - time < 2000), now];
  if (clicks.length >= 5) {
    clicks = [];
    transition?.kill();
    panel.value = "";
    mode.value = "ADMIN_AUTH_ENTRY";
  }
}
async function openProjects() {
  if (mode.value !== "HOME_AUTO") return;
  panel.value = "";
  mode.value = "TRANSITION_TO_PROJECTS";
  history.pushState({ pagehub: "projects" }, "", "#projects");
  transition?.kill();
  transition = gsap.timeline({
    onComplete: () => {
      mode.value = "PROJECTS_VIEW";
    },
  });
  transition.to(hero.value!, {
    opacity: 0,
    scale: 0.94,
    duration: 0.22,
    ease: "power2.in",
  });
}
function navigateHome(event: MouseEvent) {
  if (
    adminMode ||
    event.ctrlKey ||
    event.metaKey ||
    event.shiftKey ||
    event.altKey
  )
    return;
  event.preventDefault();
  if (mode.value === "HOME_AUTO") panel.value = "";
  else void home();
}
async function home(fromHistory = false) {
  if (
    mode.value === "TRANSITION_FROM_PROJECTS" ||
    mode.value === "TRANSITION_TO_HOME"
  )
    return;
  if (
    inProjects.value &&
    !fromHistory &&
    history.state?.pagehub === "projects"
  ) {
    history.back();
    return;
  }
  transition?.kill();
  if (inProjects.value) {
    mode.value = "TRANSITION_FROM_PROJECTS";
    await universe.value?.leave();
  }
  mode.value = "TRANSITION_TO_HOME";
  world.value?.restoreSources();
  panel.value = "";
  world.value?.automatic();
  transition = gsap.timeline({
    onComplete: () => {
      mode.value = "HOME_AUTO";
    },
  });
  transition.to(
    hero.value?.querySelectorAll(".hero-group") ?? [],
    {
      x: 0,
      y: 0,
      opacity: 1,
      duration: 0.55,
      ease: "power3.inOut",
      stagger: 0.035,
    },
    0,
  );
  transition.to(hero.value!, { opacity: 1, scale: 1, duration: 0.4 }, 0);
}
function updateGame(
  gameMode: string,
  currentScore: number,
  currentSpeed: number,
) {
  score.value = currentScore;
  speed.value = currentSpeed;
  if (!isGame.value) return;
  const modes: Record<string, Mode> = {
    manual: "GAME_RUNNING",
    paused: "GAME_PAUSED",
    dead: "GAME_DEAD",
    won: "GAME_WON",
  };
  if (modes[gameMode]) mode.value = modes[gameMode]!;
}
function checkDesktop() {
  desktop.value = matchMedia(
    "(hover: hover) and (pointer: fine) and (min-width: 768px)",
  ).matches;
  if (!desktop.value && isGame.value) {
    home();
    notice.value = "已返回自动浏览模式";
  }
}
function keyboard(event: KeyboardEvent) {
  if (
    event.isComposing ||
    event.ctrlKey ||
    event.metaKey ||
    event.altKey ||
    event.repeat ||
    panel.value ||
    (event.target instanceof HTMLElement &&
      event.target.closest("input,textarea,select,[contenteditable=true]"))
  )
    return;
  if (event.key === "Escape" && inProjects.value) {
    home();
    return;
  }
  if (!desktop.value || (!isGame.value && mode.value !== "HOME_AUTO")) return;
  const directions: Record<string, Direction> = {
    ArrowUp: "up",
    ArrowDown: "down",
    ArrowLeft: "left",
    ArrowRight: "right",
  };
  const direction = directions[event.key];
  if (direction) {
    event.preventDefault();
    if (event.shiftKey) {
      if (isGame.value && (direction === "up" || direction === "down"))
        world.value?.speed(direction === "up" ? 1 : -1);
      return;
    }
    if (mode.value === "HOME_AUTO") {
      mode.value = "GAME_RUNNING";
      world.value?.start(direction);
      const groups = hero.value!.querySelectorAll<HTMLElement>(".hero-group");
      gsap.to(groups[0]!, {
        x: -innerWidth * 0.36,
        y: -innerHeight * 0.19,
        opacity: 0.35,
        duration: 0.6,
        ease: "power3.inOut",
      });
      gsap.to(groups[1]!, {
        x: innerWidth * 0.3,
        y: -innerHeight * 0.25,
        opacity: 0.25,
        duration: 0.6,
        ease: "power3.inOut",
      });
      gsap.to(groups[2]!, {
        y: innerHeight * 0.28,
        opacity: 0.2,
        duration: 0.6,
        ease: "power3.inOut",
      });
    } else if (mode.value === "GAME_RUNNING") world.value?.turn(direction);
    return;
  }
  if (
    isGame.value &&
    (event.key.length === 1 ||
      event.key === "Escape" ||
      event.key === "Enter" ||
      event.key === "Backspace")
  )
    home();
}
function popstate() {
  if (inProjects.value) home(true);
}
function visibility() {
  if (document.hidden && mode.value === "GAME_RUNNING") world.value?.pause();
}
function fly(message: Message) {
  if (
    isGame.value ||
    !flights.value ||
    document.hidden ||
    flights.value.children.length >= 2
  )
    return;
  const line = document.createElement("span");
  line.textContent = message.content;
  line.className = "danmaku-message";
  line.style.top = `${12 + Math.floor(Math.random() * 2) * 12}%`;
  flights.value.appendChild(line);
  gsap.fromTo(
    line,
    { x: innerWidth },
    {
      x: -Math.max(400, line.offsetWidth),
      duration: 16,
      ease: "none",
      onComplete: () => line.remove(),
    },
  );
}
onMounted(() => {
  gsap.set(hero.value!, { x: 0, y: 0, xPercent: -50, yPercent: -50 });
  dockObserver = new ResizeObserver(() => {
    dockHeight.value = dock.value?.getBoundingClientRect().height ?? 80;
  });
  dockObserver.observe(dock.value!);
  checkDesktop();
  window.addEventListener("keydown", keyboard);
  window.addEventListener("resize", checkDesktop);
  window.addEventListener("popstate", popstate);
  document.addEventListener("visibilitychange", visibility);
  if (adminMode) mode.value = "ADMIN_AUTH_ENTRY";
  if (location.hash === "#projects")
    history.replaceState(null, "", location.pathname);
  motion = gsap.matchMedia();
  motion.add("(prefers-reduced-motion: reduce)", () => {
    gsap.globalTimeline.timeScale(10);
    return () => {
      gsap.globalTimeline.timeScale(1);
    };
  });
  void loadPresentation();
  void refresh();
  timer = setInterval(() => {
    if (!document.hidden && !Object.keys(busy.value).length) void refresh();
    if (!document.hidden && token.value && showAudit.value) void refreshAudit();
  }, 30_000);
});
onUnmounted(() => {
  dockObserver?.disconnect();
  window.removeEventListener("keydown", keyboard);
  window.removeEventListener("resize", checkDesktop);
  window.removeEventListener("popstate", popstate);
  document.removeEventListener("visibilitychange", visibility);
  transition?.kill();
  motion?.revert();
  if (hero.value) gsap.killTweensOf(hero.value.querySelectorAll(".hero-group"));
  if (flights.value) gsap.killTweensOf(flights.value.children);
  disposed = true;
  generation++;
  clearInterval(timer);
});
</script>

<template>
  <div
    class="portal-shell"
    :style="{ '--dock-height': `${dockHeight}px` }"
    :class="{ 'game-active': isGame, 'projects-active': inProjects }"
  >
    <SnakeWorld
      ref="world"
      :active="!inProjects && mode !== 'TRANSITION_TO_HOME'"
      @change="updateGame"
      @layout="inProjects && (sources = world?.sources(sources.length) ?? [])"
    />
    <div ref="flights" class="danmaku-layer" aria-hidden="true"></div>
    <header class="site-header">
      <a class="brand" href="/" aria-label="PageHub 首页" @click="navigateHome"
        ><span class="brand-mark"
          ><span></span><span></span><span></span><span></span></span
        >PageHub</a
      ><span class="header-caption mono">{{
        adminMode ? "LOCAL ADMIN" : "A SMALL CORNER OF THE INTERNET"
      }}</span>
    </header>
    <main
      v-show="!inProjects && !adminMode"
      ref="hero"
      class="home-hero"
      :class="{ 'is-playing': isGame }"
    >
      <div class="hero-group avatar-group">
        <button
          class="avatar-button"
          aria-label="工作室头像"
          @click="avatarClick"
        >
          <img
            :src="settings.profile.avatarPath"
            width="100"
            height="100"
            :alt="`${settings.profile.name} 的头像`"
            @error="
              ($event.target as HTMLImageElement).src = '/assets/avatar.webp'
            "
          />
        </button>
      </div>
      <div class="hero-group identity-group">
        <h1>{{ settings.profile.name }}<span class="name-dot">.</span></h1>
        <p>{{ settings.profile.signature }}</p>
      </div>
      <div class="hero-group launch-group">
        <button
          class="launch-button"
          :disabled="mode !== 'HOME_AUTO'"
          @click="openProjects"
        >
          <span
            ><span>查看项目</span
            ><span class="mono">EXPLORE PROJECTS</span></span
          ><Icon name="arrow" :size="22" />
        </button>
        <p v-if="desktop" class="keyboard-hint">
          <span class="keycap">↑</span><span class="keycap">←</span
          ><span class="keycap">↓</span><span class="keycap">→</span
          ><span>按方向键，接管这条蛇</span>
        </p>
      </div>
    </main>
    <ProjectUniverse
      v-if="inProjects"
      ref="universe"
      :apps="visibleApps"
      :sources="sources"
      @prepare="sources = world?.sources($event) ?? []"
      @close="home()"
    />
    <section v-if="isGame" class="game-interface" aria-label="桌面贪吃蛇">
      <div class="game-hud">
        <div>
          <span class="hud-label">当前得分</span
          ><strong class="mono">{{ String(score).padStart(3, "0") }}</strong>
        </div>
        <div>
          <span class="hud-label">速度</span
          ><span class="mono">{{ speed }} <small>格 / 秒</small></span>
        </div>
        <button
          class="button"
          :disabled="mode === 'GAME_DEAD' || mode === 'GAME_WON'"
          @click="world?.pause()"
        >
          <Icon
            :name="mode === 'GAME_PAUSED' ? 'play' : 'pause'"
            :size="16"
          />{{ mode === "GAME_PAUSED" ? "继续" : "暂停" }}</button
        ><button class="icon-button" aria-label="退出游戏" @click="home()">
          <Icon name="close" />
        </button>
      </div>
      <div v-if="mode !== 'GAME_RUNNING'" class="game-result">
        <h2>
          {{
            mode === "GAME_DEAD"
              ? "游戏结束"
              : mode === "GAME_WON"
                ? "已通关"
                : "稍作停留。"
          }}
        </h2>
        <p>当前局得分 {{ score }}</p>
        <div>
          <button
            v-if="mode === 'GAME_PAUSED'"
            class="button primary"
            @click="world?.pause()"
          >
            继续游戏<Icon name="play" :size="16" /></button
          ><button class="button" @click="world?.reset(true)">重新开始</button
          ><button class="text-button" @click="home()">返回首页</button>
        </div>
      </div>
      <p class="game-help mono">方向键转向 · Shift + ↑ / ↓ 调速 · Esc 退出</p>
    </section>
    <section v-if="mode === 'ADMIN_AUTH_ENTRY'" class="modal-backdrop">
      <div
        class="auth-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="admin-entry-title"
      >
        <div class="panel-heading">
          <Icon name="lock" :size="24" /><button
            v-if="!adminMode"
            class="icon-button"
            aria-label="关闭管理入口"
            @click="home()"
          >
            <Icon name="close" />
          </button>
        </div>
        <h2 id="admin-entry-title">管理员入口</h2>
        <template v-if="adminMode"
          ><p>通过本机受保护的入口连接。凭据仅保存在当前页面内存。</p>
          <form @submit.prevent="login">
            <label for="login-method">验证方式</label
            ><select id="login-method" v-model="loginMethod">
              <option value="password">管理员密码</option>
              <option value="token">兼容 Token</option></select
            ><label for="admin-token">管理员凭据</label
            ><input
              id="admin-token"
              v-model="tokenInput"
              type="password"
              autocomplete="off"
              required
              placeholder="输入管理员密码或兼容 Token"
            /><button class="button primary" :disabled="loginBusy">
              {{ loginBusy ? "验证中…" : "连接管理端"
              }}<Icon name="arrow" :size="17" />
            </button></form></template
        ><template v-else
          ><p>请通过 SSH 转发访问本机管理页面。</p>
          <code>ssh -L 8091:127.0.0.1:8091 user@server</code
          ><a
            class="button primary"
            href="http://127.0.0.1:8091/admin/"
            target="_blank"
            rel="noopener noreferrer"
            >打开本机管理端<Icon name="external" :size="16" /></a
        ></template>
        <p v-if="error" class="message error" role="alert">{{ error }}</p>
      </div>
    </section>
    <main v-if="adminMode && token" class="admin-dashboard">
      <header class="admin-heading">
        <div>
          <h1>应用管理</h1>
          <p>展示配置与实际运行状态。</p>
        </div>
        <div class="admin-controls">
          <button
            class="button"
            :disabled="busy.self"
            @click="beginPlatformUpdate"
          >
            更新平台
          </button>
          <button class="button" @click="editor = true">编辑展示</button
          ><button class="button" @click="loadAudit">
            {{ showAudit ? "收起审计" : "任务与审计" }}</button
          ><button class="button" :disabled="loading" @click="refresh()">
            刷新状态</button
          ><button class="text-button" @click="logout">退出管理</button>
        </div>
      </header>
      <MaintenancePanel
        v-if="maintenance"
        :key="maintenance.app.id + maintenance.action"
        :app="maintenance.app"
        :action="maintenance.action"
        :token="token"
        @close="maintenance = undefined"
        @submit="submitMaintenance"
      />
      <section v-if="showAudit" class="audit-panel" aria-label="任务与审计">
        <h2>最近任务与审计</h2>
        <ul>
          <li v-for="job in audit" :key="job.id">
            <span>{{ job.app_id }}</span><span>{{
              actionLabels[job.action] || job.action
            }}</span
            ><span>{{ job.state }}</span
            ><time>{{
              job.created_at
                ? new Date(job.created_at).toLocaleString("zh-CN")
                : ""
            }}</time>
            <span v-if="job.error" class="audit-receipt">{{ job.error }}</span>
            <code v-if="job.result?.backup" class="audit-receipt"
              >已验证备份 {{ job.result.backup }}</code
            >
            <code v-if="job.commit" class="audit-receipt">{{
              job.commit
            }}</code>
            <details v-if="job.changes?.length" class="audit-details">
              <summary>计划与版本</summary>
              <pre>{{ JSON.stringify(job.changes, null, 2) }}</pre>
            </details>
          </li>
        </ul>
        <p v-if="!audit.length">暂无任务记录。</p>
      </section>
      <div class="toolbar">
        <label class="search"
          ><Icon name="search" :size="17" /><input
            v-model="query"
            type="search"
            placeholder="搜索应用"
            aria-label="搜索管理应用" /></label
        ><select v-model="kind" aria-label="应用类型">
          <option value="all">全部类型</option>
          <option value="static">静态站点</option>
          <option value="image">单镜像</option>
          <option value="compose">Compose</option>
          <option value="external">外部服务</option>
          <option value="external-compose">既有 Compose</option>
        </select>
      </div>
      <AppCatalog
        :apps="filtered"
        :managed="true"
        :busy="busy"
        @action="action"
        @maintenance="beginMaintenance"
      />
      <p v-if="!filtered.length" class="empty-copy">没有匹配的应用。</p>
    </main>
    <div v-if="editor && token" class="modal-backdrop">
      <ProjectManager
        :settings="settings"
        :apps="apps"
        :token="token"
        @close="editor = false"
        @reload="settings = $event"
        @saved="
          settings = $event;
          editor = false;
          notice = '展示配置已保存';
        "
      />
    </div>
    <div
      v-if="(error && mode !== 'ADMIN_AUTH_ENTRY') || notice || snapshot"
      class="notice-stack"
      role="status"
    >
      <p v-if="error" class="message error">
        {{ error }}<button class="text-button" @click="refresh()">重试</button>
      </p>
      <p v-if="notice" class="message">
        {{ notice
        }}<button
          class="icon-button"
          aria-label="关闭通知"
          @click="notice = ''"
        >
          <Icon name="close" :size="14" />
        </button>
      </p>
      <p v-if="snapshot" class="message">静态清单模式 · 实时状态暂不可确认</p>
    </div>
    <aside
      v-if="panel === 'status'"
      class="side-panel status-panel"
      aria-label="服务状态"
    >
      <div class="panel-heading">
        <div>
          <h2>服务状态</h2>
          <p>
            {{ snapshot ? "暂不可确认" : `${healthy} 个运行正常`
            }}<span v-if="lastUpdated"> · {{ lastUpdated }} 更新</span>
          </p>
        </div>
        <button
          class="icon-button"
          aria-label="关闭服务状态"
          @click="panel = ''"
        >
          <Icon name="close" />
        </button>
      </div>
      <ul class="service-list">
        <li v-for="app in visibleApps" :key="app.id">
          <a :href="app.path" target="_blank" rel="noopener">{{ app.name }}</a><span
            class="status"
            :data-state="app.state"
            ><i></i>{{ stateLabels[app.state] }}</span
          >
        </li>
      </ul>
      <p v-if="!visibleApps.length" class="empty-copy">
        暂无已公开且通过离线验证的项目。
      </p>
      <button class="text-button" :disabled="loading" @click="refresh()">
        {{ loading ? "刷新中…" : "刷新状态"
        }}<Icon name="reset" :size="15" /></button
      ><button class="text-button" @click="showCatalog = !showCatalog">
        {{ showCatalog ? "收起应用列表" : "打开简单应用列表" }}</button
      ><AppCatalog
        v-if="showCatalog"
        :apps="visibleApps"
        :managed="false"
        :busy="{}"
      />
    </aside>
    <DanmakuPanel
      :open="panel === 'social'"
      :token="token"
      @close="panel = ''"
      @message="fly"
    />
    <footer ref="dock" class="bottom-dock">
      <a
        class="github-link"
        :href="settings.profile.githubUrl"
        target="_blank"
        rel="noopener noreferrer"
        ><Icon name="github" :size="18" /><span>double-cloth-studio</span
        ><Icon class="dock-external" name="external" :size="12" /></a
      ><button
        class="service-summary mono"
        :aria-expanded="panel === 'status'"
        @click="panel = panel === 'status' ? '' : 'status'"
      >
        <i :class="{ healthy: healthy > 0 }"></i
        ><span
          >{{ String(healthy).padStart(2, "0") }}
          <span class="muted"
            >/ {{ String(visibleApps.length).padStart(2, "0") }}</span
          >
          ONLINE</span
        ><Icon name="up" :size="13" /></button
      ><button
        class="danmaku-toggle"
        :aria-expanded="panel === 'social'"
        @click="panel = panel === 'social' ? '' : 'social'"
      >
        <span>弹幕</span
        ><Icon :name="panel === 'social' ? 'close' : 'plus'" :size="17" />
      </button>
    </footer>
  </div>
</template>
