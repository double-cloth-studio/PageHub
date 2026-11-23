<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from "vue";
import { gsap } from "gsap";
import { SnakeEngine, type Direction } from "./engine";

const props = defineProps<{ active: boolean }>();
const emit = defineEmits<{
  change: [mode: string, score: number, speed: number];
  layout: [];
}>();
const host = ref<HTMLDivElement>();
let engine: SnakeEngine;
let context: CanvasRenderingContext2D | null = null;
let background: CanvasRenderingContext2D | null = null;
let resizeFrame = 0;
let last = 0;
let accumulator = 0;
let cellWidth = 22;
let cellHeight = 22;
let boardWidth = 0;
let boardHeight = 0;
let pulse = 0;
let observer: ResizeObserver;
let colorPhase = 0;
const liftedCells = new Set<string>();
const colors = { tile: "#171d22", snake: "#2b363f" };
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
function gap() {
  const side = Math.min(cellWidth, cellHeight);
  return Math.min(side / 3, Math.max(0.7, side * 0.085));
}

function report() {
  emit("change", engine.mode, engine.score, engine.speed);
}
function resize() {
  if (!host.value) return;
  const width = host.value.clientWidth;
  const height = host.value.clientHeight;
  if (width < 1 || height < 1) return;
  let targetCell = Math.max(
    width < 768 ? 18 : 12,
    Math.sqrt((width * height) / 3200),
  );
  if (engine) {
    const points = engine.food ? [...engine.cells, engine.food] : engine.cells;
    const xs = points.map((point) => point.x);
    const ys = points.map((point) => point.y);
    const spanX = Math.max(...xs) - Math.min(...xs) + 1;
    const spanY = Math.max(...ys) - Math.min(...ys) + 1;
    // 缩窄窗口时等比缩小网格，确保整条蛇仍在合法棋盘内。
    targetCell = Math.min(
      targetCell,
      width / (spanX + (spanX % 2)),
      height / spanY,
    );
  }
  const columns = Math.max(4, Math.floor(width / targetCell / 2) * 2);
  const rows = Math.max(4, Math.floor(height / targetCell));
  if (!engine) engine = new SnakeEngine(columns, rows);
  else if (engine.columns !== columns || engine.rows !== rows)
    engine.resize(columns, rows);
  cellWidth = width / engine.columns;
  cellHeight = height / engine.rows;
  report();
  boardWidth = width;
  boardHeight = height;
  const density = Math.min(devicePixelRatio, 2);
  for (const layer of [context, background]) {
    if (!layer) continue;
    layer.canvas.width = Math.round(width * density);
    layer.canvas.height = Math.round(height * density);
    layer.setTransform(
      layer.canvas.width / width, 0, 0, layer.canvas.height / height, 0, 0,
    );
  }
  drawGrid();
  draw();
  emit("layout");
}

function tileGlow(x: number, y: number) {
  return pulse * (0.35 + 0.65 * ((x + 2 * y) % 7) / 6);
}

function drawGrid() {
  if (!background || !engine) return;
  background.clearRect(0, 0, boardWidth, boardHeight);
  const spacing = gap();
  const tileWidth = cellWidth - spacing;
  const tileHeight = cellHeight - spacing;
  background.fillStyle = colors.tile;
  for (let y = 0; y < engine.rows; y++) {
    for (let x = 0; x < engine.columns; x++) {
      if (liftedCells.size && liftedCells.has(`${x},${y}`)) continue;
      background.fillRect(x * cellWidth, y * cellHeight, tileWidth, tileHeight);
    }
  }
  if (!pulse) return;
  background.fillStyle = "#6b9b95";
  for (let y = 0; y < engine.rows; y++) {
    for (let x = 0; x < engine.columns; x++) {
      if (liftedCells.size && liftedCells.has(`${x},${y}`)) continue;
      background.globalAlpha = tileGlow(x, y);
      background.fillRect(x * cellWidth, y * cellHeight, tileWidth, tileHeight);
    }
  }
  background.globalAlpha = 1;
}
function draw() {
  if (!context || !background || !engine || !boardWidth) return;
  context.clearRect(0, 0, boardWidth, boardHeight);
  context.drawImage(background.canvas, 0, 0, boardWidth, boardHeight);
  const spacing = gap();
  const tileWidth = cellWidth - spacing;
  const tileHeight = cellHeight - spacing;
  context.fillStyle = colors.snake;
  for (const point of engine.cells)
    context.fillRect(
      point.x * cellWidth,
      point.y * cellHeight,
      tileWidth,
      tileHeight,
    );
  const head = engine.cells[0]!;
  if (tileWidth > 1 && tileHeight > 1) {
    context.strokeStyle = "#6e7b83";
    context.strokeRect(
      head.x * cellWidth + 0.5,
      head.y * cellHeight + 0.5,
      tileWidth - 1,
      tileHeight - 1,
    );
  }
  if (engine.food) {
    context.fillStyle = "#f04444";
    context.fillRect(
      engine.food.x * cellWidth, engine.food.y * cellHeight, tileWidth, tileHeight,
    );
  }
}
function clock() {
  const now = performance.now();
  const elapsed = Math.min(now - last, 180);
  last = now;
  if (!engine || !props.active || document.hidden) {
    return;
  }
  accumulator += elapsed;
  const interval = 1000 / (engine.isAutomatic ? 12 : engine.speed);
  if (accumulator < interval) return;
  accumulator %= interval;
  engine.tick();
  const glow = reducedMotion.matches ? 0 : 0.05 * (1 - Math.cos(now / 1700));
  if (Math.abs(glow - pulse) >= 0.002 || (glow === 0 && pulse !== 0)) {
    pulse = glow;
    drawGrid();
  }
  if (engine.colorPhase !== colorPhase) {
    colorPhase = engine.colorPhase;
    gsap.to(colors, {
      tile: colorPhase ? "#2b363f" : "#171d22",
      snake: colorPhase ? "#171d22" : "#2b363f",
      duration: reducedMotion.matches ? 0 : 0.65,
      overwrite: true,
      onUpdate: () => {
        drawGrid();
        draw();
      },
    });
  }
  draw();
  report();
}
function start(direction: Direction) {
  engine?.manual(direction);
  report();
}
function turn(direction: Direction) {
  engine?.turn(direction);
}
function speed(delta: number) {
  engine.speed = Math.max(5, Math.min(16, engine.speed + delta));
  report();
}
function pause() {
  if (engine.mode !== "manual" && engine.mode !== "paused") return;
  engine.mode = engine.mode === "paused" ? "manual" : "paused";
  report();
}
function reset(manual = false) {
  engine.reset(manual ? "manual" : "auto");
  draw();
  report();
}
function automatic() {
  engine?.automatic();
  report();
}
function sources(count: number) {
  if (!engine) return [];
  const occupied = new Set(
    engine.cells.map((point) => `${point.x},${point.y}`),
  );
  if (engine.food) occupied.add(`${engine.food.x},${engine.food.y}`);
  const rect = host.value!.getBoundingClientRect();
  const projectBounds = document
    .querySelector(".project-universe")
    ?.getBoundingClientRect();
  const visibleBottom = (projectBounds?.bottom ?? rect.bottom) - rect.y;
  const available = engine.route.filter(
    (point) =>
      !occupied.has(`${point.x},${point.y}`) &&
      point.y * cellHeight >= 100 &&
      (point.y + 1) * cellHeight <= visibleBottom - 48,
  );
  const spacing = gap();
  liftedCells.clear();
  const selected = Array.from(
    { length: Math.min(count, available.length) },
    () => {
      const point = available.splice(
        Math.floor(Math.random() * available.length),
        1,
      )[0]!;
      liftedCells.add(`${point.x},${point.y}`);
      return {
        x: rect.x + point.x * cellWidth,
        y: rect.y + point.y * cellHeight,
        width: cellWidth - spacing,
        height: cellHeight - spacing,
        color: gsap.utils.interpolate(
          colors.tile, "#6b9b95", tileGlow(point.x, point.y),
        ),
      };
    },
  );
  drawGrid();
  draw();
  return selected;
}

function restoreSources() {
  liftedCells.clear();
  drawGrid();
  draw();
}

defineExpose({
  start,
  turn,
  speed,
  pause,
  reset,
  automatic,
  sources,
  restoreSources,
});
watch(
  () => props.active,
  () => {
    last = performance.now();
  },
);
onMounted(() => {
  const canvas = document.createElement("canvas");
  context = canvas.getContext("2d");
  background = document.createElement("canvas").getContext("2d");
  if (context && background) {
    host.value!.classList.remove("static-grid");
    host.value!.appendChild(canvas);
  }
  observer = new ResizeObserver(() => {
    cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(resize);
  });
  observer.observe(host.value!);
  resize();
  last = performance.now();
  gsap.ticker.add(clock);
});
onUnmounted(() => {
  cancelAnimationFrame(resizeFrame);
  observer?.disconnect();
  gsap.ticker.remove(clock);
  gsap.killTweensOf(colors);
});
</script>
<template>
  <div ref="host" class="snake-world static-grid" aria-hidden="true"></div>
</template>
