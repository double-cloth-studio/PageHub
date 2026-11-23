import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import ts from "typescript";

async function loadModule(path) {
  const source = await readFile(new URL(path, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ES2022,
    },
  });
  return import(
    `data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`
  );
}
const { SnakeEngine, hamiltonian } = await loadModule(
  "../src/features/snake/engine.ts",
);
const { limitProjectPan, projectLayout } = await loadModule(
  "../src/features/projects/project-layout.ts",
);
const { searchProjects } = await loadModule(
  "../src/features/projects/project-search.ts",
);

test("闭合回路覆盖偶数列棋盘，每一步均相邻", () => {
  for (const [columns, rows] of [
    [4, 4],
    [6, 7],
    [40, 25],
    [64, 33],
  ]) {
    const route = hamiltonian(columns, rows);
    assert.equal(route.length, columns * rows);
    assert.equal(
      new Set(route.map((cell) => `${cell.x},${cell.y}`)).size,
      route.length,
    );
    route.forEach((cell, index) => {
      const next = route[(index + 1) % route.length];
      assert.equal(Math.abs(cell.x - next.x) + Math.abs(cell.y - next.y), 1);
    });
  }
});
test("多尺寸与随机种子下自动蛇持续生存、反色与回收", () => {
  for (const [columns, rows] of [
    [4, 4],
    [5, 7],
    [10, 8],
    [40, 25],
  ]) {
    for (let seed = 1; seed <= 6; seed++) {
      let value = seed;
      const random = () => {
        value = (value * 16807) % 2147483647;
        return 0.002 + 0.998 * (value - 1) / 2147483646;
      };
      const engine = new SnakeEngine(columns, rows, random);
      let flipped = false;
      for (let tick = 0; tick < 25000; tick++) {
        engine.tick();
        assert.equal(
          engine.mode,
          "auto",
          `尺寸 ${columns}×${rows}，种子 ${seed}，步数 ${tick}`,
        );
        assert.equal(
          new Set(engine.cells.map((cell) => `${cell.x},${cell.y}`)).size,
          engine.cells.length,
        );
        assert.ok(
          !engine.food ||
            !engine.cells.some(
              (cell) => cell.x === engine.food.x && cell.y === engine.food.y,
            ),
        );
        if (engine.colorPhase) flipped = true;
      }
      assert.ok(engine.score > 0);
      if (columns <= 10) assert.ok(flipped);
    }
  }
});
test("成长阈值触发单次反色，回收后继续增长", () => {
  const engine = new SnakeEngine(20, 20, () => 0);
  const length = engine.maxGrowthLength;
  engine.cells = engine.route
    .slice(0, length)
    .reverse()
    .map((cell) => ({ ...cell }));
  engine.food = { ...engine.route[length] };
  engine.tick();
  assert.equal(engine.colorPhase, 1);
  assert.equal(engine.recycling, true);
  for (let i = 0; i < length * 4 && engine.recycling; i++) engine.tick();
  assert.equal(engine.mode, "auto");
  assert.equal(engine.colorPhase, 1);
  assert.ok(engine.cells.length < length);
  assert.equal(engine.recycling, false);
});
test("随机冒险可触发撞墙，自动等待后重启，手动死亡不重启", () => {
  let roll = 0.5;
  const engine = new SnakeEngine(8, 8, () => roll);
  // 强制下一次随机选择向上，蛇头此时位于上边缘。
  engine.cells = [{ x: 4, y: 0 }, { x: 3, y: 0 }];
  roll = 0;
  engine.tick();
  assert.equal(engine.mode, "dead");
  assert.equal(engine.isAutomatic, true);
  for (let step = 0; step < 11; step++) engine.tick();
  assert.equal(engine.mode, "dead");
  engine.tick();
  assert.equal(engine.mode, "auto");
  assert.equal(engine.cells.length, engine.initialLength);
  assert.equal(engine.score, 0);
  engine.cells = [{ x: 4, y: 0 }, { x: 3, y: 0 }];
  engine.direction = "up";
  engine.manual("up");
  engine.tick();
  for (let step = 0; step < 30; step++) engine.tick();
  assert.equal(engine.mode, "dead");
  assert.equal(engine.isAutomatic, false);
});
test("手动游戏禁止掉头、同一步双转，正确计算合法尾格、撞墙与胜利", () => {
  const engine = new SnakeEngine(4, 4, () => 0);
  engine.cells = [
    { x: 1, y: 1 },
    { x: 0, y: 1 },
    { x: 0, y: 2 },
  ];
  engine.direction = "right";
  engine.mode = "manual";
  engine.food = { x: 3, y: 3 };
  engine.turn("left");
  assert.equal(engine.direction, "right");
  engine.turn("up");
  engine.turn("left");
  assert.equal(engine.direction, "up");
  engine.tick();
  engine.tick();
  assert.equal(engine.mode, "dead");
  engine.reset("manual");
  engine.cells = [
    { x: 1, y: 1 },
    { x: 1, y: 2 },
    { x: 0, y: 2 },
    { x: 0, y: 1 },
  ];
  engine.direction = "left";
  engine.food = { x: 3, y: 3 };
  engine.tick();
  assert.equal(engine.mode, "manual");
  engine.reset("manual");
  const route = engine.route;
  engine.cells = route.slice(1).reverse();
  engine.direction = "left";
  engine.food = route[0];
  engine.tick();
  assert.equal(engine.mode, "won");
  assert.equal(engine.score, 1);
});
test("不同视口按实际首屏容量布局，外围坐标保持确定", () => {
  for (const [width, height] of [
    [320, 497],
    [390, 773],
    [844, 331],
    [1440, 822],
    [3840, 2082],
  ]) {
    for (const count of [0, 1, 5, 6, 7, 20, 100]) {
      const first = projectLayout(count, width, height);
      assert.deepEqual(first, projectLayout(count, width, height));
      assert.equal(first.points.length, count);
      assert.equal(
        new Set(first.points.map((point) => `${point.x},${point.y}`)).size,
        count,
      );
      first.points.slice(0, first.initialCount).forEach((point) => {
        assert.ok(Math.abs(point.x) + first.size / 2 < width / 2);
        assert.ok(Math.abs(point.y) + first.size / 2 < height / 2);
      });
      first.points
        .slice(first.initialCount)
        .forEach((point) =>
          assert.ok(
            Math.abs(point.x) > width / 2 || Math.abs(point.y) > height / 2,
          ),
        );
    }
  }
});

test("接管、返回自动和实时缩放均保留蛇身与游戏数据", () => {
  const engine = new SnakeEngine(64, 40, () => 0.4);
  for (let i = 0; i < 100; i++) engine.tick();
  engine.speed = 11;
  const cells = structuredClone(engine.cells);
  const food = structuredClone(engine.food);
  const score = engine.score;
  const phase = engine.colorPhase;
  engine.manual(engine.direction);
  engine.mode = "paused";
  engine.resize(20, 80);
  assert.equal(engine.mode, "paused");
  assert.equal(engine.score, score);
  assert.equal(engine.speed, 11);
  assert.equal(engine.colorPhase, phase);
  assert.equal(engine.cells.length, cells.length);
  const dx = engine.cells[0].x - cells[0].x;
  const dy = engine.cells[0].y - cells[0].y;
  assert.deepEqual(
    engine.cells,
    cells.map((cell) => ({ x: cell.x + dx, y: cell.y + dy })),
  );
  assert.deepEqual(engine.food, { x: food.x + dx, y: food.y + dy });
  engine.automatic();
  assert.equal(engine.score, score);
  assert.equal(engine.speed, 11);
  const before = structuredClone(engine.cells);
  engine.automatic();
  assert.deepEqual(engine.cells, before);
  for (let i = 0; i < 1000; i++) {
    engine.tick();
    assert.equal(engine.mode, "auto");
  }
});

test("连续改变棋盘尺寸后蛇身相邻、边界合法且自动运行不中断", () => {
  let seed = 123;
  const random = () => ((seed = (seed * 16807) % 2147483647) - 1) / 2147483646;
  const engine = new SnakeEngine(64, 40, () => 0.002 + 0.998 * random());
  for (let round = 0; round < 60; round++) {
    const length = engine.cells.length;
    const score = engine.score;
    engine.resize(
      4 + Math.floor(random() * 40) * 2,
      4 + Math.floor(random() * 70),
    );
    assert.equal(engine.cells.length, length);
    assert.equal(engine.score, score);
    for (let step = 0; step < 50; step++) {
      engine.tick();
      assert.equal(engine.mode, "auto", `第 ${round} 次缩放，第 ${step} 步`);
      assert.equal(
        new Set(engine.cells.map((cell) => `${cell.x},${cell.y}`)).size,
        engine.cells.length,
      );
      engine.cells.forEach((cell, index) => {
        assert.ok(
          cell.x >= 0 &&
            cell.x < engine.columns &&
            cell.y >= 0 &&
            cell.y < engine.rows,
        );
        if (index)
          assert.equal(
            Math.abs(cell.x - engine.cells[index - 1].x) +
              Math.abs(cell.y - engine.cells[index - 1].y),
            1,
          );
      });
    }
  }
});

test("首屏项目数量随空间改变，支持超过六项及少量项目", () => {
  assert.ok(projectLayout(30, 1440, 822).initialCount > 6);
  assert.ok(projectLayout(30, 320, 490).initialCount < 6);
  assert.equal(projectLayout(3, 1440, 822).initialCount, 3);
  assert.equal(projectLayout(0, 1440, 822).initialCount, 0);
});

test("极端拖动与缩放后至少一张卡片仍有可见区域", () => {
  for (const [width, height] of [[320, 490], [1440, 822]]) {
    for (const count of [1, 8, 30]) {
      const layout = projectLayout(count, width, height);
      for (const scale of [0.6, 1, 2.2]) {
        const size = layout.size * scale;
        for (const x of [-20000, 0, 20000]) {
          for (const y of [-20000, 0, 20000]) {
            const pan = limitProjectPan(x, y, scale, width, height, layout);
            assert.ok(layout.points.some((point) => {
              const left = (width / 2 + point.x - layout.size / 2) * scale + pan.x;
              const top = (layout.centerY + point.y - layout.size / 2) * scale + pan.y;
              const horizontal = Math.min(left + size, width) - Math.max(left, 0);
              const vertical = Math.min(top + size, height - 68) - Math.max(top, width < 600 ? 128 : 100);
              return horizontal >= 16 && vertical >= 16;
            }));
          }
        }
      }
    }
  }
});

test("搜索按输入即时筛选，支持全角文字、少字及相邻字符误拼", () => {
  const diary = { name: "Travel Diary", description: "记录旅途见闻", path: "/diary/" };
  const calculator = { name: "Whalgebra", path: "/calc/", keywords: ["数学工具"] };
  const projects = [diary, calculator];
  assert.deepEqual(searchProjects(projects, "ｔｒａｖｌ 见闻"), [diary]);
  assert.deepEqual(searchProjects(projects, "whalgerba 数学"), [calculator]);
  assert.deepEqual(searchProjects(projects, "暂无匹配"), []);
  assert.deepEqual(searchProjects(projects, "  "), projects);
});
