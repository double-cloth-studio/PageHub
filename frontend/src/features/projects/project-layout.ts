export function projectLayout(count: number, width: number, height: number) {
  const gap = 18;
  const availableWidth = Math.max(1, width - 48);
  const topInset = width < 600 ? 128 : 100;
  const bottomInset = 68;
  const availableHeight = Math.max(1, height - topInset - bottomInset);
  const centerY = topInset + availableHeight / 2;
  const preferredSize = width < 600 ? 120 : 176;
  const columnCapacity = Math.max(
    1,
    Math.floor((availableWidth + gap) / (preferredSize + gap)),
  );
  const rowCapacity = Math.max(
    1,
    Math.floor((availableHeight + gap) / (preferredSize + gap)),
  );
  const initialCount = Math.min(count, columnCapacity * rowCapacity);
  const columns = Math.max(
    1,
    Math.min(
      columnCapacity,
      Math.ceil(Math.sqrt((initialCount * availableWidth) / availableHeight)),
    ),
  );
  const rows = Math.max(1, Math.ceil(initialCount / columns));
  const size = Math.max(
    32,
    Math.min(
      208,
      (availableWidth - (columns - 1) * gap) / columns,
      (availableHeight - (rows - 1) * gap) / rows,
    ),
  );
  const pitch = size + Math.max(12, Math.min(22, size * 0.1));
  const points: { x: number; y: number }[] = [];
  for (let i = 0; i < initialCount; i++) {
    const items = Math.min(
      columns,
      initialCount - Math.floor(i / columns) * columns,
    );
    points.push({
      x: ((i % columns) - (items - 1) / 2) * pitch,
      y: (Math.floor(i / columns) - (rows - 1) / 2) * pitch,
    });
  }
  // 首屏容量随视口变化，其余项目按确定顺序在外围扩展。
  let ring = 1;
  while (points.length < count) {
    const radiusX = width / 2 + pitch * (ring - 0.2);
    const radiusY = Math.max(height / 2, pitch * 2) + pitch * (ring - 0.2);
    for (let side = 0; side < 4 && points.length < count; side++) {
      for (let step = -ring; step <= ring && points.length < count; step++) {
        points.push(
          side % 2 === 0
            ? { x: side === 0 ? radiusX : -radiusX, y: step * pitch }
            : { x: step * pitch, y: side === 1 ? radiusY : -radiusY },
        );
      }
    }
    ring++;
  }
  return { size, points, initialCount, centerY };
}

export function limitProjectPan(
  x: number,
  y: number,
  scale: number,
  width: number,
  height: number,
  layout: ReturnType<typeof projectLayout>,
) {
  const size = layout.size * scale;
  const visible = Math.min(size / 2, 32, width / 2, height / 2);
  const top = Math.min(width < 600 ? 128 : 100, height / 2);
  const bottom = Math.max(top + visible, height - 68);
  let closest = { x: 0, y: 0 };
  let distance = Infinity;
  for (const point of layout.points) {
    const left = (width / 2 + point.x) * scale - size / 2;
    const upper = (layout.centerY + point.y) * scale - size / 2;
    const nextX = Math.max(
      visible - left - size,
      Math.min(x, width - visible - left),
    );
    const nextY = Math.max(
      top + visible - upper - size,
      Math.min(y, bottom - visible - upper),
    );
    const delta = (nextX - x) ** 2 + (nextY - y) ** 2;
    if (!delta) return { x, y };
    if (delta < distance) {
      closest = { x: nextX, y: nextY };
      distance = delta;
    }
  }
  return closest;
}
