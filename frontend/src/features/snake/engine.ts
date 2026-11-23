export type Cell = { x: number; y: number };
export type Direction = "up" | "down" | "left" | "right";
export type GameMode = "auto" | "manual" | "paused" | "dead" | "won";
const vectors: Record<Direction, Cell> = {
  up: { x: 0, y: -1 },
  down: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
  right: { x: 1, y: 0 },
};
const directions = Object.values(vectors);
const automaticRestartDelay = 12;
export const same = (a: Cell, b: Cell) => a.x === b.x && a.y === b.y;

// 偶数列棋盘的闭合回路，预留最上行作为返回通道。
export function hamiltonian(columns: number, rows: number): Cell[] {
  const route: Cell[] = [{ x: 0, y: 0 }];
  for (let x = 0; x < columns; x++) {
    for (let y = 1; y < rows; y++) route.push({ x, y: x % 2 ? rows - y : y });
  }
  for (let x = columns - 1; x > 0; x--) route.push({ x, y: 0 });
  return route;
}

export class SnakeEngine {
  cells: Cell[] = [];
  food: Cell | null = null;
  mode: GameMode = "auto";
  direction: Direction = "down";
  score = 0;
  speed = 8;
  colorPhase = 0;
  recycling = false;
  private turned = false;
  private steps = 0;
  route: Cell[];
  indices: Map<string, number>;
  private ordered = true;
  private restartTicks = 0;

  constructor(
    public columns: number,
    public rows: number,
    private random = Math.random,
  ) {
    this.columns = Math.max(4, columns - (columns % 2));
    this.rows = Math.max(4, rows);
    this.route = hamiltonian(this.columns, this.rows);
    this.indices = new Map(
      this.route.map((cell, index) => [`${cell.x},${cell.y}`, index]),
    );
    this.reset();
  }

  get initialLength() {
    return Math.min(36, Math.max(5, Math.floor(this.route.length * 0.025)));
  }
  get maxGrowthLength() {
    return Math.max(
      this.initialLength,
      Math.min(320, Math.floor(this.route.length * 0.35)),
    );
  }
  get isAutomatic() {
    return this.mode === "auto" || this.restartTicks > 0;
  }

  reset(mode: GameMode = "auto") {
    const head = Math.floor(this.route.length * 0.38);
    this.cells = Array.from({ length: this.initialLength }, (_, i) => ({
      ...this.route[(head - i + this.route.length) % this.route.length]!,
    }));
    this.mode = mode;
    this.score = 0;
    this.speed = 8;
    this.recycling = false;
    this.turned = false;
    this.ordered = true;
    this.steps = 0;
    this.restartTicks = 0;
    this.setDirection(this.route[(head + 1) % this.route.length]!);
    this.placeFood();
  }

  manual(direction: Direction) {
    this.restartTicks = 0;
    this.mode = "manual";
    this.ordered = false;
    this.turned = false;
    this.turn(direction);
  }

  automatic() {
    if (this.restartTicks > 0) return;
    this.mode = "auto";
    this.turned = false;
    this.ordered = this.isOrdered();
  }

  // 尺寸改变只平移已有蛇身，保留形状、长度、分数和游戏状态。
  resize(columns: number, rows: number) {
    const points = this.food ? [...this.cells, this.food] : this.cells;
    const xs = points.map((cell) => cell.x);
    const ys = points.map((cell) => cell.y);
    const minX = Math.min(...xs);
    const minY = Math.min(...ys);
    columns = Math.max(4, columns, Math.max(...xs) - minX + 1);
    columns += columns % 2;
    rows = Math.max(4, rows, Math.max(...ys) - minY + 1);
    const dx = Math.min(0, columns - 1 - Math.max(...xs)) - Math.min(0, minX);
    const dy = Math.min(0, rows - 1 - Math.max(...ys)) - Math.min(0, minY);
    this.columns = columns;
    this.rows = rows;
    this.cells = this.cells.map((cell) => ({ x: cell.x + dx, y: cell.y + dy }));
    if (this.food) this.food = { x: this.food.x + dx, y: this.food.y + dy };
    this.route = hamiltonian(columns, rows);
    this.indices = new Map(
      this.route.map((cell, index) => [`${cell.x},${cell.y}`, index]),
    );
    if (this.food && !this.indices.has(`${this.food.x},${this.food.y}`))
      this.placeFood();
    this.ordered = this.isOrdered();
  }

  private isOrdered() {
    let distance = 0;
    for (let i = this.cells.length - 1; i > 0; i--) {
      const tail = this.indices.get(`${this.cells[i]!.x},${this.cells[i]!.y}`)!;
      const head = this.indices.get(
        `${this.cells[i - 1]!.x},${this.cells[i - 1]!.y}`,
      )!;
      distance += (head - tail + this.route.length) % this.route.length;
    }
    return distance < this.route.length;
  }

  private path(start: Cell, target: Cell, body: Cell[]) {
    const key = (cell: Cell) => cell.y * this.columns + cell.x;
    const blocked = new Set(body.map(key));
    blocked.delete(key(start));
    blocked.delete(key(target));
    const queue = [start];
    const parents = new Map<number, Cell | null>([[key(start), null]]);
    for (let cursor = 0; cursor < queue.length; cursor++) {
      const point = queue[cursor]!;
      if (same(point, target)) {
        const path: Cell[] = [];
        let current: Cell | null = point;
        while (current && !same(current, start)) {
          path.unshift(current);
          current = parents.get(key(current))!;
        }
        return path;
      }
      for (const vector of directions) {
        const next = { x: point.x + vector.x, y: point.y + vector.y };
        if (
          next.x < 0 ||
          next.x >= this.columns ||
          next.y < 0 ||
          next.y >= this.rows ||
          blocked.has(key(next)) ||
          parents.has(key(next))
        )
          continue;
        parents.set(key(next), point);
        queue.push(next);
      }
    }
    return [];
  }

  // 手动接管和缩放后，先沿可达尾部的安全路径行进，再接回闭合回路。
  private freeNext(head: Cell): Cell {
    if (this.food) {
      const path = this.path(head, this.food, this.cells.slice(0, -1));
      if (path.length) {
        const future = this.cells.map((cell) => ({ ...cell }));
        for (const point of path) {
          future.unshift(point);
          if (!same(point, this.food) || this.recycling) future.pop();
        }
        if (
          this.path(future[0]!, future[future.length - 1]!, future.slice(0, -1))
            .length
        )
          return path[0]!;
      }
    }
    const tail = this.path(
      head,
      this.cells[this.cells.length - 1]!,
      this.cells.slice(0, -1),
    );
    if (tail.length) return tail[0]!;
    return (
      directions
        .map((vector) => ({ x: head.x + vector.x, y: head.y + vector.y }))
        .find(
          (point) =>
            point.x >= 0 &&
            point.x < this.columns &&
            point.y >= 0 &&
            point.y < this.rows &&
            !this.cells.slice(0, -1).some((cell) => same(cell, point)),
        ) ?? head
    );
  }

  turn(direction: Direction) {
    if (this.turned) return;
    const current = vectors[this.direction];
    const next = vectors[direction];
    if (current.x + next.x === 0 && current.y + next.y === 0) return;
    this.direction = direction;
    this.turned = true;
  }

  private setDirection(next: Cell) {
    const head = this.cells[0]!;
    this.direction =
      next.x > head.x
        ? "right"
        : next.x < head.x
          ? "left"
          : next.y > head.y
            ? "down"
            : "up";
  }

  private placeFood() {
    const occupied = new Set(this.cells.map((cell) => `${cell.x},${cell.y}`));
    const free = this.route.filter(
      (cell) => !occupied.has(`${cell.x},${cell.y}`),
    );
    this.food = free.length
      ? { ...free[Math.floor(this.random() * free.length)]! }
      : null;
  }

  private autoNext(head: Cell, roll: number): Cell {
    if (!this.ordered) {
      this.ordered = this.isOrdered();
      if (!this.ordered) return this.freeNext(head);
    }
    const capacity = this.route.length;
    const index = this.indices.get(`${head.x},${head.y}`)!;
    const fallback = this.route[(index + 1) % capacity]!;
    if (roll < 0.15) return fallback;
    if (!this.food || this.cells.length > capacity * 0.3) return fallback;
    const tail = this.cells[this.cells.length - 1]!;
    const tailIndex = this.indices.get(`${tail.x},${tail.y}`)!;
    const foodIndex = this.indices.get(`${this.food.x},${this.food.y}`)!;
    const distance = (target: number) => (target - index + capacity) % capacity;
    const occupied = new Set(this.cells.map((cell) => `${cell.x},${cell.y}`));
    const candidates = directions.map((vector) => ({
      x: head.x + vector.x,
      y: head.y + vector.y,
    }));
    let best = fallback;
    let advance = 1;
    for (const candidate of candidates) {
      const candidateIndex = this.indices.get(`${candidate.x},${candidate.y}`);
      if (
        candidateIndex === undefined ||
        occupied.has(`${candidate.x},${candidate.y}`)
      )
        continue;
      const step = distance(candidateIndex);
      // 头部只能沿回路向前越过空格，保留尾部前方余量且不越过食物。
      if (
        step > advance &&
        step < distance(tailIndex) - 2 &&
        step <= distance(foodIndex)
      ) {
        best = candidate;
        advance = step;
      }
    }
    return best;
  }

  tick() {
    if (this.restartTicks > 0) {
      this.restartTicks--;
      if (this.restartTicks === 0) this.reset();
      return;
    }
    if (this.mode !== "auto" && this.mode !== "manual") return;
    const head = this.cells[0]!;
    let next: Cell;
    if (this.mode === "auto") {
      const roll = this.random();
      // 偶尔尝试邻格，其余时间沿安全路径移动。
      if (roll < 0.002) {
        const vector = directions[Math.floor((roll / 0.002) * directions.length)]!;
        next = { x: head.x + vector.x, y: head.y + vector.y };
        this.ordered = false;
      } else {
        next = this.autoNext(head, roll);
      }
      this.setDirection(next);
    } else {
      const vector = vectors[this.direction];
      next = { x: head.x + vector.x, y: head.y + vector.y };
    }
    const eating = !!this.food && same(next, this.food);
    const body = eating ? this.cells : this.cells.slice(0, -1);
    if (
      next.x < 0 ||
      next.x >= this.columns ||
      next.y < 0 ||
      next.y >= this.rows ||
      body.some((cell) => same(cell, next))
    ) {
      if (this.mode === "auto") this.restartTicks = automaticRestartDelay;
      this.mode = "dead";
      return;
    }
    this.cells.unshift({ ...next });
    if (!eating || this.recycling) this.cells.pop();
    if (eating) {
      this.score++;
      this.placeFood();
    }
    this.steps++;
    if (this.mode === "auto") {
      if (!this.recycling && this.cells.length >= this.maxGrowthLength) {
        this.recycling = true;
        this.colorPhase = 1 - this.colorPhase;
      }
      if (
        this.recycling &&
        this.steps % 3 === 0 &&
        this.cells.length > this.initialLength
      )
        this.cells.pop();
      if (this.recycling && this.cells.length <= this.initialLength)
        this.recycling = false;
    } else if (!this.food) this.mode = "won";
    this.turned = false;
  }
}
