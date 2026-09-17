const graph = document.getElementById("graph");
const inspector = document.getElementById("inspector");
const selectionEl = document.getElementById("selection");
const legendEl = document.getElementById("legend");
const contextEl = document.getElementById("context");
const statsEl = document.getElementById("stats");
const statusEl = document.getElementById("connectionStatus");
const warningsEl = document.getElementById("warnings");
const emptyEl = document.getElementById("graphEmpty");
const graphHintEl = document.querySelector(".graph-hint");
const selectionTab = document.getElementById("selectionTab");
const legendTab = document.getElementById("legendTab");
const selectionPanel = document.getElementById("selectionPanel");
const legendPanel = document.getElementById("legendPanel");
const namespaceFilter = document.getElementById("namespaceFilter");
const groupFilter = document.getElementById("groupFilter");
const autoRefreshToggle = document.getElementById("autoRefresh");
const showGatewayToggle = document.getElementById("showGateway");
const showPodLinesToggle = document.getElementById("showPodLines");
const zoomOutButton = document.getElementById("zoomOut");
const zoomResetButton = document.getElementById("zoomReset");
const zoomInButton = document.getElementById("zoomIn");

const groupColors = [
  "#2563eb",
  "#16a34a",
  "#dc2626",
  "#9333ea",
  "#c2410c",
  "#0891b2",
  "#be123c",
  "#4f46e5",
  "#0f766e",
  "#a16207",
];

const nodeRadius = 82;

let refreshMs = 2000;
let latestSnapshot = null;
let autoRefresh = true;
let selectedNamespace = "all";
let selectedGroup = "all";
let showGateway = showGatewayToggle.checked;
let showPodLines = showPodLinesToggle.checked;
let zoom = 1;
let viewCenter = null;
let panState = null;
let suppressGraphClick = false;
let nodeDragState = null;
let zoneDragState = null;
let nodePositionOverrides = new Map();
let nodePositionContext = "";
let fittedContext = null;

init();

async function init() {
  namespaceFilter.addEventListener("change", () => {
    selectedNamespace = namespaceFilter.value;
    selectedGroup = "all";
    if (latestSnapshot) {
      syncFilters(latestSnapshot);
      render(latestSnapshot);
    }
  });
  groupFilter.addEventListener("change", () => {
    selectedGroup = groupFilter.value;
    if (latestSnapshot) render(latestSnapshot);
  });
  autoRefreshToggle.addEventListener("change", () => {
    autoRefresh = autoRefreshToggle.checked;
    setConnectionStatus(autoRefresh ? "Connected" : "Paused", autoRefresh ? "live" : "paused");
    if (autoRefresh) refresh();
  });
  selectionTab.addEventListener("click", () => setInspectorTab("selection"));
  legendTab.addEventListener("click", () => setInspectorTab("legend"));
  showGatewayToggle.addEventListener("change", () => {
    showGateway = showGatewayToggle.checked;
    if (latestSnapshot) render(latestSnapshot);
  });
  showPodLinesToggle.addEventListener("change", () => {
    showPodLines = showPodLinesToggle.checked;
    if (latestSnapshot) render(latestSnapshot);
  });
  zoomOutButton.addEventListener("click", () => changeZoom(zoom / 1.25));
  zoomResetButton.addEventListener("click", () => fitView());
  zoomInButton.addEventListener("click", () => changeZoom(zoom * 1.25));
  graph.addEventListener("wheel", handleWheelZoom, { passive: false });
  graph.addEventListener("click", suppressClickAfterPan, true);
  graph.addEventListener("pointerdown", startPan);
  graph.addEventListener("pointermove", movePan);
  graph.addEventListener("pointerup", endPan);
  graph.addEventListener("pointercancel", endPan);

  try {
    const config = await fetchJSON("/api/config");
    refreshMs = Number(config.refreshMs) || refreshMs;
  } catch (error) {
    console.warn(error);
  }
  await refresh();
  setInterval(() => {
    if (autoRefresh) refresh();
  }, refreshMs);
  window.addEventListener("resize", () => {
    if (latestSnapshot) render(latestSnapshot);
  });
}

async function refresh() {
  try {
    const snapshot = await fetchJSON("/api/snapshot");
    latestSnapshot = snapshot;
    setConnectionStatus(autoRefresh ? "Connected" : "Paused", autoRefresh ? "live" : "paused");
    syncFilters(snapshot);
    if (fittedContext !== snapshot.context) {
      fittedContext = snapshot.context;
      fitView(snapshot);
    } else {
      render(snapshot);
    }
  } catch (error) {
    setConnectionStatus("Unavailable", "error");
    contextEl.textContent = "Snapshot could not be loaded";
    warningsEl.textContent = error.message;
    warningsEl.hidden = false;
  }
}

function setConnectionStatus(label, state) {
  statusEl.textContent = label;
  statusEl.dataset.state = state;
}

function setInspectorTab(tab) {
  const details = tab === "selection";
  selectionTab.setAttribute("aria-selected", String(details));
  legendTab.setAttribute("aria-selected", String(!details));
  selectionPanel.hidden = !details;
  legendPanel.hidden = details;
}

async function fetchJSON(url) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(await response.text());
  }
  return response.json();
}

function render(snapshot) {
  const {width, height} = graphDimensions();
  updateViewBox(width, height);
  clear(graph);

  const visiblePods = filterPods(snapshot.pods);
  const compact = visiblePods.length > 20 && zoom < 1.2;
  graph.classList.toggle("compact", compact);
  graphHintEl.textContent = compact ? "Zoom in or filter to show pod labels · Select an element for details" : "Drag to pan · Scroll to zoom · Select a node or pod";
  const podsByNode = groupBy(visiblePods, (pod) => pod.node);
  const nodePositions = layoutNodes(snapshot.nodes, width, height, visiblePods);
  applyNodePositionOverrides(snapshot.context, nodePositions, width, height);
  const podPositions = layoutPods(visiblePods, podsByNode, nodePositions);
  const appPositions = layoutApps(visiblePods, podPositions);
  const zoneEdges = aggregateZoneEdges(snapshot.nodeEdges, snapshot.nodes);
  const zonePositions = zoneCenters(snapshot.nodes, nodePositions);

  contextEl.textContent = `${snapshot.context} · ${new Date(snapshot.generatedAt).toLocaleTimeString()}`;
  statsEl.innerHTML = [
    pill(snapshot.nodes.length, "Nodes"),
    pill(visiblePods.length, "Pods"),
    pill(zoneEdges.length, "Zone links"),
    pill(countGroups(visiblePods), "Groups"),
  ].join("");
  warningsEl.hidden = !snapshot.warnings?.length;
  warningsEl.textContent = snapshot.warnings?.join(" · ") || "";
  emptyEl.hidden = snapshot.nodes.length > 0;
  renderLegend(visiblePods);

  drawZones(snapshot.nodes, nodePositions, visiblePods);
  drawZoneEdges(zoneEdges, zonePositions);
  if (showGateway) drawGatewayTraffic(visiblePods, snapshot.appEdges, nodePositions, appPositions, width, height);
  if (showPodLines) drawAppEdges(snapshot.appEdges, appPositions);
  drawNodes(snapshot.nodes, nodePositions, podsByNode);
  drawPods(visiblePods, podPositions, nodePositions);
}

function layoutNodes(nodes, width, height, pods = []) {
  const positions = new Map();
  const zones = groupBy(nodes, (node) => node.zone || "unassigned");
  if (!zones.size) return positions;
  const zoneLayouts = [...zones.entries()].map(([zone, zoneNodes]) => {
    const ordered = [...zoneNodes].sort((a, b) => a.name.localeCompare(b.name));
    const columns = Math.ceil(Math.sqrt(ordered.length));
    const rows = Math.ceil(ordered.length / columns);
    const spacing = 294;
    const local = new Map();
    ordered.forEach((node, index) => local.set(node.name, {
      x: (index % columns - (columns - 1) / 2) * spacing,
      y: (Math.floor(index / columns) - (rows - 1) / 2) * spacing,
    }));
    return {zone, ordered, local, bounds: zoneBounds(ordered, local, pods)};
  }).sort((left, right) => right.ordered.length - left.ordered.length || left.zone.localeCompare(right.zone));
  const columns = Array.from({length: width < 700 ? 1 : Math.ceil(Math.sqrt(zoneLayouts.length))}, () => ({layouts: [], width: 0, height: 0}));
  const gap = 36;
  for (const layout of zoneLayouts) {
    const column = columns.reduce((best, candidate) => candidate.height < best.height ? candidate : best);
    column.layouts.push(layout);
    column.width = Math.max(column.width, layout.bounds.width);
    column.height += layout.bounds.height + (column.layouts.length > 1 ? gap : 0);
  }
  const totalWidth = columns.reduce((sum, column) => sum + column.width, 0) + gap * (columns.length - 1);
  const totalHeight = Math.max(...columns.map((column) => column.height));
  let columnX = (width - totalWidth) / 2;
  for (const column of columns) {
    let rowY = (height - totalHeight) / 2 + (totalHeight - column.height) / 2;
    for (const layout of column.layouts) {
      const offsetX = columnX + (column.width - layout.bounds.width) / 2 - layout.bounds.minX;
      const offsetY = rowY - layout.bounds.minY;
      for (const [name, point] of layout.local.entries()) positions.set(name, {x: point.x + offsetX, y: point.y + offsetY});
      rowY += layout.bounds.height + gap;
    }
    columnX += column.width + gap;
  }
  return positions;
}

function zoneBounds(nodes, nodePositions, pods) {
  const points = nodes.map((node) => nodePositions.get(node.name)).filter(Boolean);
  let minX = Math.min(...points.map((point) => point.x - nodeRadius));
  let maxX = Math.max(...points.map((point) => point.x + nodeRadius));
  let minY = Math.min(...points.map((point) => point.y - nodeRadius));
  let maxY = Math.max(...points.map((point) => point.y + nodeRadius));
  const nodeNames = new Set(nodes.map((node) => node.name));
  const zonePods = pods.filter((pod) => nodeNames.has(pod.node));
  const podPositions = layoutPods(zonePods, groupBy(zonePods, (pod) => pod.node), nodePositions);
  for (const pod of zonePods) {
    const point = podPositions.get(podKey(pod));
    if (!point) continue;
    minX = Math.min(minX, point.x - 13);
    maxX = Math.max(maxX, point.x + 13);
    minY = Math.min(minY, point.y - 13);
    maxY = Math.max(maxY, point.y + 13);
    const label = podLabelLayout(pod, point, nodePositions.get(pod.node));
    const textWidth = truncateLabel(pod.name, 16).length * 6.5;
    minX = Math.min(minX, label.x - (label.anchor === "end" ? textWidth : label.anchor === "middle" ? textWidth / 2 : 0));
    maxX = Math.max(maxX, label.x + (label.anchor === "start" ? textWidth : label.anchor === "middle" ? textWidth / 2 : 0));
    minY = Math.min(minY, label.y - 12);
    maxY = Math.max(maxY, label.y + 4);
  }
  const margin = 28;
  minX -= margin;
  maxX += margin;
  minY -= margin + 50;
  maxY += margin;
  return {minX, maxX, minY, maxY, width: maxX - minX, height: maxY - minY};
}

function zoneCenters(nodes, nodePositions) {
  const centers = new Map();
  const byZone = groupBy(nodes, (node) => node.zone || "unassigned");
  for (const [zone, zoneNodes] of byZone.entries()) {
    const points = zoneNodes.map((node) => nodePositions.get(node.name)).filter(Boolean);
    if (points.length === 0) continue;
    centers.set(zone, {
      x: points.reduce((sum, point) => sum + point.x, 0) / points.length,
      y: points.reduce((sum, point) => sum + point.y, 0) / points.length,
    });
  }
  return centers;
}

function drawZones(nodes, nodePositions, pods) {
  const byZone = groupBy(nodes, (node) => node.zone || "unassigned");
  for (const [zone, zoneNodes] of byZone.entries()) {
    if (!zoneNodes.some((node) => nodePositions.has(node.name))) continue;
    const {minX, maxX, minY, maxY} = zoneBounds(zoneNodes, nodePositions, pods);
    const boundary = svg("rect", {
      x: minX,
      y: minY,
      width: maxX - minX,
      height: maxY - minY,
      rx: 24,
      class: "zone-boundary",
    });
    boundary.addEventListener("pointerdown", (event) => startZoneDrag(event, zone, zoneNodes, nodePositions));
    graph.append(boundary);
    const headerWidth = Math.min(maxX - minX - 28, Math.max(96, zone.length * 7.5 + 28));
    graph.append(svg("rect", {
      x: minX + 14,
      y: minY + 12,
      width: headerWidth,
      height: 30,
      rx: 15,
      class: "zone-label-background",
    }));
    const maxLabelCharacters = Math.max(4, Math.floor((headerWidth - 28) / 7.5));
    const visibleZoneLabel = zone.length > maxLabelCharacters ? `${zone.slice(0, maxLabelCharacters - 1)}…` : zone;
    graph.append(svgText(minX + 28, minY + 32, visibleZoneLabel, "zone-label", "start"));
  }
}

function layoutPods(pods, podsByNode, nodePositions) {
  const positions = new Map();
  for (const [node, nodePods] of podsByNode.entries()) {
    const center = nodePositions.get(node);
    if (!center) continue;
    const radius = nodeRadius + 34;
    const sorted = [...nodePods].sort((a, b) => a.name.localeCompare(b.name));
    sorted.forEach((pod, index) => {
      const angle = sorted.length === 1 ? Math.PI / 2 : (Math.PI * 2 * index) / sorted.length + Math.PI / 2;
      positions.set(podKey(pod), {
        x: center.x + Math.cos(angle) * radius,
        y: center.y + Math.sin(angle) * radius,
      });
    });
  }
  return positions;
}

function layoutApps(pods, podPositions) {
  const pointsByApp = new Map();
  for (const pod of pods) {
    const pos = podPositions.get(podKey(pod));
    if (!pos || !pod.app) continue;
    const key = `${pod.namespace}/${pod.app}`;
    if (!pointsByApp.has(key)) pointsByApp.set(key, []);
    pointsByApp.get(key).push(pos);
  }
  const appPositions = new Map();
  for (const [app, points] of pointsByApp.entries()) {
    appPositions.set(app, {
      x: points.reduce((sum, point) => sum + point.x, 0) / points.length,
      y: points.reduce((sum, point) => sum + point.y, 0) / points.length,
    });
  }
  return appPositions;
}

function drawZoneEdges(edges, zonePositions) {
  for (const edge of edges) {
    const source = zonePositions.get(edge.source);
    const target = zonePositions.get(edge.target);
    if (!source || !target) continue;
    const curve = Math.abs(source.x - target.x) < 100 ? -95 : 0;
    const path = svg("path", {
      d: curvedPath(source, target, curve),
      class: "latency-edge edge-hit",
    });
    path.addEventListener("pointerdown", stopGraphPan);
    path.addEventListener("click", () => inspect("Zone Link", edge));
    const tooltip = svg("title");
    tooltip.textContent = `${edge.source} → ${edge.target}${edge.label ? ` · ${edge.label}` : ""}`;
    path.append(tooltip);
    graph.append(path);

    const label = curvePoint(source, target, curve);
    graph.append(svgText(label.x, label.y - 8, edge.label || "", "edge-label"));
  }
}

function drawAppEdges(edges, appPositions) {
  for (const edge of edges) {
    const source = appPositions.get(edge.source);
    const target = appPositions.get(edge.target);
    if (!source || !target) continue;
    const path = svg("path", {
      d: curvedPath(source, target, 34),
      class: "app-edge edge-hit",
    });
    path.addEventListener("pointerdown", stopGraphPan);
    path.addEventListener("click", () => inspect("App Link", edge));
    graph.append(path);
  }
}

function drawNodes(nodes, nodePositions, podsByNode) {
  for (const node of nodes) {
    const pos = nodePositions.get(node.name);
    if (!pos) continue;
    const group = svg("g", { class: "node-hit" });
    group.addEventListener("click", () => inspect("Node", node));
    group.addEventListener("pointerdown", (event) => startNodeDrag(event, node.name, pos));
    group.append(svg("circle", {
      cx: pos.x,
      cy: pos.y,
      r: nodeRadius,
      class: `node-circle ${node.ready ? "" : "not-ready"}`,
    }));
    const labelLines = nodeLabelLines(node.name);
    labelLines.forEach((line, index) => {
      const y = pos.y - 31 + index * 13 + (labelLines.length === 1 ? 6 : 0);
      group.append(svgText(pos.x, y, line, "node-label", "middle"));
    });
    const resourcesAvailable = Number.isFinite(node.cpu) || Number.isFinite(node.memory);
    const networkAvailable = Number.isFinite(node.network);
    group.append(svgText(pos.x, pos.y - 2, resourcesAvailable ? `${formatMillicores(node.cpu)} · ${formatBytes(node.memory)}` : "Metrics unavailable", "metric-label", "middle"));
    if (networkAvailable || resourcesAvailable) group.append(svgText(pos.x, pos.y + 17, networkAvailable ? `${formatRate(node.network)} net` : "Network unavailable", "metric-label", "middle"));
    group.append(svgText(pos.x, pos.y + 36, `${(podsByNode.get(node.name) || []).length} pods`, "metric-label", "middle"));
    graph.append(group);
  }
}

function drawPods(pods, podPositions, nodePositions) {
  for (const pod of pods) {
    const pos = podPositions.get(podKey(pod));
    if (!pos) continue;
    const group = svg("g", {class: "pod-hit"});
    group.addEventListener("pointerdown", startPodSelect);
    group.addEventListener("pointerup", (event) => endPodSelect(event, pod));
    group.addEventListener("pointercancel", endPodSelect);
    group.append(svg("circle", {
      cx: pos.x,
      cy: pos.y,
      r: 13,
      fill: podColor(pod),
      class: "pod",
    }));
    const tooltip = svg("title");
    tooltip.textContent = `${pod.namespace}/${pod.name}`;
    group.append(tooltip);
    if (pod.index) {
      group.append(svgText(pos.x, pos.y + 3, truncateLabel(pod.index, 4), "pod-index", "middle"));
    }
    graph.append(group);
    graph.append(podLabelText(pod, pos, nodePositions.get(pod.node)));
  }
}

// Labels are anchored along the node->pod radial direction (rather than always
// e.g. below the circle) so that pods fanned around the same node fan their
// labels apart too, instead of stacking into one unreadable cluster.
function podLabelLayout(pod, pos, nodeCenter) {
  const labelOffset = 13 + 9;
  let x = pos.x;
  let y = pos.y + labelOffset + 3;
  let anchor = "middle";
  if (nodeCenter) {
    const dx = pos.x - nodeCenter.x;
    const dy = pos.y - nodeCenter.y;
    const length = Math.hypot(dx, dy) || 1;
    const ux = dx / length;
    const uy = dy / length;
    x = pos.x + ux * labelOffset;
    y = pos.y + uy * labelOffset + 3;
    anchor = ux > 0.35 ? "start" : ux < -0.35 ? "end" : "middle";
  }
  return {x, y, anchor};
}

function podLabelText(pod, pos, nodeCenter) {
  const {x, y, anchor} = podLabelLayout(pod, pos, nodeCenter);
  return svgText(x, y, truncateLabel(pod.name, 16), "pod-label", anchor);
}

function truncateLabel(text, maxChars) {
  return text.length > maxChars ? `${text.slice(0, maxChars - 1)}…` : text;
}

function detectGatewayTargets(pods, appEdges) {
  const gatewayPattern = /gateway|ingress|proxy|envoy|nginx|traefik|haproxy/i;
  const found = new Set();
  for (const pod of pods) {
    const name = pod.app || pod.name || "";
    if (gatewayPattern.test(name)) found.add(`${pod.namespace}/${pod.app || pod.name}`);
  }
  if (found.size > 0) return [...found];

  if (appEdges && appEdges.length > 0) {
    const sources = new Set(appEdges.map((e) => e.source));
    const entryPoints = [...new Set(appEdges.map((e) => e.target))].filter((t) => !sources.has(t));
    if (entryPoints.length > 0) return entryPoints;
  }

  return [];
}

function drawGatewayTraffic(visiblePods, appEdges, nodePositions, appPositions, width, height) {
  const gatewayKeys = detectGatewayTargets(visiblePods, appEdges);
  let targetPositions = gatewayKeys.map((key) => appPositions.get(key)).filter(Boolean);
  if (targetPositions.length === 0) targetPositions = [...nodePositions.values()].slice(0, 3);
  if (targetPositions.length === 0) return;

  const centerX = width / 2;
  const centerY = height / 2;
  const avgTarget = {
    x: targetPositions.reduce((s, p) => s + p.x, 0) / targetPositions.length,
    y: targetPositions.reduce((s, p) => s + p.y, 0) / targetPositions.length,
  };

  const dx = avgTarget.x - centerX;
  const dy = avgTarget.y - centerY;
  const dist = Math.hypot(dx, dy) || 1;
  const dirX = dx / dist;
  const dirY = dy / dist;
  const perpX = -dirY;
  const perpY = dirX;

  const spread = 28;
  const extraDist = 90;
  const userPositions = [-1, 0, 1].map((i) => ({
    x: avgTarget.x + dirX * extraDist + perpX * (i * spread),
    y: avgTarget.y + dirY * extraDist + perpY * (i * spread),
  }));

  const defs = svg("defs");
  const marker = svg("marker", { id: "gateway-arrow", markerWidth: "8", markerHeight: "8", refX: "6", refY: "3", orient: "auto" });
  marker.append(svg("path", { d: "M 0 0 L 6 3 L 0 6 z", fill: "#0891b2" }));
  defs.append(marker);
  graph.prepend(defs);

  const centerUser = userPositions[1];
  for (const target of targetPositions) {
    graph.append(svg("path", {
      d: curvedPath(centerUser, target, 18),
      class: "gateway-edge",
      "marker-end": "url(#gateway-arrow)",
    }));
  }

  graph.append(svgText(centerUser.x, centerUser.y - 26, "Users", "gateway-label", "middle"));
  for (const pos of userPositions) {
    const g = svg("g", { class: "user-icon" });
    g.append(svg("circle", { cx: pos.x, cy: pos.y - 8, r: 5.5 }));
    g.append(svg("path", { d: `M ${pos.x - 10} ${pos.y + 14} C ${pos.x - 10} ${pos.y + 3} ${pos.x + 10} ${pos.y + 3} ${pos.x + 10} ${pos.y + 14}` }));
    graph.append(g);
  }
}

function inspect(title, value) {
  selectionEl.classList.remove("selection-empty");
  selectionEl.innerHTML = `<h2>${escapeHTML(title)}</h2><div class="detail">${detailRows({ kind: title, ...value })}</div>`;
  setInspectorTab("selection");
}

function syncFilters(snapshot) {
  const namespaces = uniqueSorted(snapshot.pods.map((pod) => pod.namespace).filter(Boolean));
  selectedNamespace = namespaces.includes(selectedNamespace) || selectedNamespace === "all" ? selectedNamespace : "all";
  setOptions(namespaceFilter, [
    { value: "all", label: "All namespaces" },
    ...namespaces.map((namespace) => ({ value: namespace, label: namespace })),
  ], selectedNamespace);

  const namespacePods = snapshot.pods.filter((pod) => selectedNamespace === "all" || pod.namespace === selectedNamespace);
  const groups = uniqueSorted(namespacePods.map((pod) => pod.group).filter(Boolean));
  const hasUngrouped = namespacePods.some((pod) => !pod.group);
  const groupOptions = [
    { value: "all", label: "All groups" },
    ...groups.map((group) => ({ value: group, label: group })),
  ];
  if (hasUngrouped) groupOptions.push({ value: "__unlabeled", label: "Unlabeled" });
  selectedGroup = groupOptions.some((option) => option.value === selectedGroup) ? selectedGroup : "all";
  setOptions(groupFilter, groupOptions, selectedGroup);
}

function setOptions(select, options, selected) {
  const current = Array.from(select.options).map((option) => `${option.value}:${option.textContent}`).join("|");
  const next = options.map((option) => `${option.value}:${option.label}`).join("|");
  if (current !== next) {
    select.innerHTML = options
      .map((option) => `<option value="${escapeHTML(option.value)}">${escapeHTML(option.label)}</option>`)
      .join("");
  }
  select.value = selected;
}

function filterPods(pods) {
  return pods.filter((pod) => {
    if (selectedNamespace !== "all" && pod.namespace !== selectedNamespace) return false;
    if (selectedGroup === "all") return true;
    if (selectedGroup === "__unlabeled") return !pod.group;
    return pod.group === selectedGroup;
  });
}

function aggregateZoneEdges(edges, nodes) {
  const nodeZones = new Map(nodes.map((node) => [node.name, node.zone || "unassigned"]));
  const pairs = new Map();
  for (const edge of edges) {
    const sourceZone = nodeZones.get(edge.source);
    const targetZone = nodeZones.get(edge.target);
    if (!sourceZone || !targetZone || sourceZone === targetZone) continue;
    const zones = [sourceZone, targetZone].sort((a, b) => a.localeCompare(b));
    const key = zones.join("::");
    if (!pairs.has(key)) {
      pairs.set(key, {
        source: zones[0],
        target: zones[1],
        kind: "network",
        values: [],
        bandwidthValues: [],
        packetLossValues: [],
      });
    }
    const pair = pairs.get(key);
    if (Number.isFinite(edge.value)) pair.values.push(edge.value);
    if (Number.isFinite(edge.bandwidth)) pair.bandwidthValues.push(edge.bandwidth);
    if (Number.isFinite(edge.packetLoss)) pair.packetLossValues.push(edge.packetLoss);
  }

  return [...pairs.values()].map((pair) => {
    const value = average(pair.values);
    const bandwidth = average(pair.bandwidthValues);
    const packetLoss = average(pair.packetLossValues);
    const label = [
      Number.isFinite(value) ? formatLatency(value) : "",
      Number.isFinite(bandwidth) ? formatRate(bandwidth) : "",
      Number.isFinite(packetLoss) ? formatPacketLoss(packetLoss) : "",
    ].filter(Boolean).join(" · ");
    return {
      source: pair.source,
      target: pair.target,
      kind: pair.kind,
      value,
      bandwidth,
      packetLoss,
      samples: Math.max(pair.values.length, pair.bandwidthValues.length, pair.packetLossValues.length),
      label,
    };
  });
}

function average(values) {
  if (values.length === 0) return null;
  return values.reduce((sum, next) => sum + next, 0) / values.length;
}

function changeZoom(nextZoom, focus = null) {
  const rect = graph.getBoundingClientRect();
  const {width, height} = graphDimensions();
  const next = clamp(nextZoom, 0.15, 4);

  if (focus) {
    viewCenter = zoomAroundPoint(focus, next, rect, width, height);
  } else {
    viewCenter = currentViewCenter(width, height);
  }

  zoom = next;
  if (latestSnapshot) render(latestSnapshot);
}

function fitView(snapshot = latestSnapshot) {
  if (!snapshot) return;
  const {width, height} = graphDimensions();
  const visiblePods = filterPods(snapshot.pods);
  const positions = layoutNodes(snapshot.nodes, width, height, visiblePods);
  applyNodePositionOverrides(snapshot.context, positions, width, height);
  if (!positions.size) return;
  const bounds = [...groupBy(snapshot.nodes, (node) => node.zone || "unassigned").values()].map((zoneNodes) => zoneBounds(zoneNodes, positions, visiblePods));
  const minX = Math.min(...bounds.map((item) => item.minX));
  const maxX = Math.max(...bounds.map((item) => item.maxX));
  const minY = Math.min(...bounds.map((item) => item.minY));
  const maxY = Math.max(...bounds.map((item) => item.maxY));
  viewCenter = { x: (minX + maxX) / 2, y: (minY + maxY) / 2 };
  zoom = clamp(Math.min(width / (maxX - minX), height / (maxY - minY)) * 0.92, 0.15, 3);
  render(snapshot);
}

function handleWheelZoom(event) {
  event.preventDefault();
  const factor = event.deltaY < 0 ? 1.16 : 1 / 1.16;
  changeZoom(zoom * factor, { x: event.clientX, y: event.clientY });
}

function startPan(event) {
  if (event.button !== 0) return;
  if (nodeDragState || zoneDragState) return;
  panState = {
    pointerId: event.pointerId,
    x: event.clientX,
    y: event.clientY,
    moved: false,
  };
  graph.setPointerCapture(event.pointerId);
  graph.classList.add("is-panning");
}

function movePan(event) {
  if (!panState || panState.pointerId !== event.pointerId) return;
  const dx = event.clientX - panState.x;
  const dy = event.clientY - panState.y;
  panState.x = event.clientX;
  panState.y = event.clientY;
  if (Math.hypot(dx, dy) > 2) panState.moved = true;
  panBy(dx, dy);
}

function endPan(event) {
  if (!panState || panState.pointerId !== event.pointerId) return;
  suppressGraphClick = panState.moved;
  graph.releasePointerCapture(event.pointerId);
  graph.classList.remove("is-panning");
  panState = null;
  if (suppressGraphClick) window.setTimeout(() => {
    suppressGraphClick = false;
  }, 0);
}

function suppressClickAfterPan(event) {
  if (!suppressGraphClick) return;
  event.preventDefault();
  event.stopPropagation();
  suppressGraphClick = false;
}

function stopGraphPan(event) {
  event.stopPropagation();
}

function startPodSelect(event) {
  if (event.button !== 0) return;
  event.stopPropagation();
  event.currentTarget.setPointerCapture(event.pointerId);
}

function endPodSelect(event, pod = null) {
  event.stopPropagation();
  if (event.currentTarget.hasPointerCapture(event.pointerId)) {
    event.currentTarget.releasePointerCapture(event.pointerId);
  }
  if (pod) inspect("Pod", pod);
}

function panBy(dx, dy) {
  const rect = graph.getBoundingClientRect();
  const {width, height} = graphDimensions();
  const center = currentViewCenter(width, height);
  const viewWidth = width / zoom;
  const viewHeight = height / zoom;
  const scaleX = rect.width > 0 ? viewWidth / rect.width : 1;
  const scaleY = rect.height > 0 ? viewHeight / rect.height : 1;

  viewCenter = {
    x: center.x - dx * scaleX,
    y: center.y - dy * scaleY,
  };
  updateViewBox(width, height);
}

function graphDimensions() {
  const rect = graph.getBoundingClientRect();
  return {
    width: rect.width < 700 ? Math.max(rect.width, 420) : Math.max(rect.width, 900),
    height: Math.max(rect.height, 620),
  };
}

function startNodeDrag(event, nodeName, position) {
  if (event.button !== 0) return;
  event.stopPropagation();
  const point = clientToGraphPoint(event.clientX, event.clientY);
  nodeDragState = {
    pointerId: event.pointerId,
    nodeName,
    nodeValue: latestSnapshot?.nodes.find((node) => node.name === nodeName),
    moved: false,
    offsetX: point.x - position.x,
    offsetY: point.y - position.y,
  };
  graph.setPointerCapture(event.pointerId);
  graph.classList.add("is-dragging-node");
  graph.addEventListener("pointermove", moveNodeDrag);
  graph.addEventListener("pointerup", endNodeDrag);
  graph.addEventListener("pointercancel", endNodeDrag);
}

function moveNodeDrag(event) {
  if (!nodeDragState || nodeDragState.pointerId !== event.pointerId) return;
  event.stopPropagation();
  const point = clientToGraphPoint(event.clientX, event.clientY);
  const x = point.x - nodeDragState.offsetX;
  const y = point.y - nodeDragState.offsetY;
  nodeDragState.moved = true;
  nodePositionOverrides.set(nodeDragState.nodeName, { x, y });
  saveNodePositionOverrides();
  if (latestSnapshot) render(latestSnapshot);
}

function endNodeDrag(event) {
  if (!nodeDragState || nodeDragState.pointerId !== event.pointerId) return;
  event.stopPropagation();
  graph.releasePointerCapture(event.pointerId);
  graph.classList.remove("is-dragging-node");
  graph.removeEventListener("pointermove", moveNodeDrag);
  graph.removeEventListener("pointerup", endNodeDrag);
  graph.removeEventListener("pointercancel", endNodeDrag);
  const wasMoved = nodeDragState.moved;
  const nodeValue = nodeDragState.nodeValue;
  nodeDragState = null;
  if (wasMoved) {
    event.preventDefault();
  } else if (nodeValue) {
    inspect("Node", nodeValue);
  }
}

function startZoneDrag(event, zone, zoneNodes, nodePositions) {
  if (event.button !== 0) return;
  event.stopPropagation();
  const positions = new Map();
  for (const node of zoneNodes) {
    const position = nodePositions.get(node.name);
    if (position) positions.set(node.name, { ...position });
  }
  if (positions.size === 0) return;

  zoneDragState = {
    pointerId: event.pointerId,
    zone,
    start: clientToGraphPoint(event.clientX, event.clientY),
    positions,
    moved: false,
  };
  graph.setPointerCapture(event.pointerId);
  graph.classList.add("is-dragging-zone");
  graph.addEventListener("pointermove", moveZoneDrag);
  graph.addEventListener("pointerup", endZoneDrag);
  graph.addEventListener("pointercancel", endZoneDrag);
}

function moveZoneDrag(event) {
  if (!zoneDragState || zoneDragState.pointerId !== event.pointerId) return;
  event.stopPropagation();
  const point = clientToGraphPoint(event.clientX, event.clientY);
  const dx = point.x - zoneDragState.start.x;
  const dy = point.y - zoneDragState.start.y;

  zoneDragState.moved = Math.abs(dx) > 1 || Math.abs(dy) > 1;
  for (const [nodeName, position] of zoneDragState.positions.entries()) {
    nodePositionOverrides.set(nodeName, { x: position.x + dx, y: position.y + dy });
  }
  if (latestSnapshot) render(latestSnapshot);
}

function endZoneDrag(event) {
  if (!zoneDragState || zoneDragState.pointerId !== event.pointerId) return;
  event.stopPropagation();
  if (graph.hasPointerCapture(event.pointerId)) graph.releasePointerCapture(event.pointerId);
  graph.classList.remove("is-dragging-zone");
  graph.removeEventListener("pointermove", moveZoneDrag);
  graph.removeEventListener("pointerup", endZoneDrag);
  graph.removeEventListener("pointercancel", endZoneDrag);
  const wasMoved = zoneDragState.moved;
  zoneDragState = null;
  if (wasMoved) {
    saveNodePositionOverrides();
    event.preventDefault();
  }
}

function clientToGraphPoint(clientX, clientY) {
  const rect = graph.getBoundingClientRect();
  const viewBox = graph.viewBox.baseVal;
  const ratioX = rect.width > 0 ? (clientX - rect.left) / rect.width : 0.5;
  const ratioY = rect.height > 0 ? (clientY - rect.top) / rect.height : 0.5;
  return {
    x: viewBox.x + ratioX * viewBox.width,
    y: viewBox.y + ratioY * viewBox.height,
  };
}

function applyNodePositionOverrides(context, nodePositions, width, height) {
  ensureNodePositionContext(context);
  for (const [nodeName, position] of nodePositionOverrides.entries()) {
    if (!nodePositions.has(nodeName)) continue;
    nodePositions.set(nodeName, {
      x: Number(position.x),
      y: Number(position.y),
    });
  }
}

function ensureNodePositionContext(context) {
  if (nodePositionContext === context) return;
  nodePositionContext = context || "default";
  nodePositionOverrides = loadNodePositionOverrides();
}

function nodePositionsStorageKey() {
  return `cluster-lens.node-positions.${nodePositionContext || "default"}`;
}

function loadNodePositionOverrides() {
  try {
    const stored = window.localStorage.getItem(nodePositionsStorageKey());
    if (!stored) return new Map();
    return new Map(Object.entries(JSON.parse(stored)));
  } catch (error) {
    console.warn(error);
    return new Map();
  }
}

function saveNodePositionOverrides() {
  try {
    window.localStorage.setItem(
      nodePositionsStorageKey(),
      JSON.stringify(Object.fromEntries(nodePositionOverrides.entries())),
    );
  } catch (error) {
    console.warn(error);
  }
}

function updateViewBox(width, height) {
  const center = currentViewCenter(width, height);
  const viewWidth = width / zoom;
  const viewHeight = height / zoom;
  graph.setAttribute("viewBox", `${center.x - viewWidth / 2} ${center.y - viewHeight / 2} ${viewWidth} ${viewHeight}`);
}

function currentViewCenter(width, height) {
  const fallback = { x: width / 2, y: height / 2 };
  return viewCenter || fallback;
}

function zoomAroundPoint(focus, nextZoom, rect, width, height) {
  const currentCenter = currentViewCenter(width, height);
  const currentViewWidth = width / zoom;
  const currentViewHeight = height / zoom;
  const nextViewWidth = width / nextZoom;
  const nextViewHeight = height / nextZoom;
  const ratioX = rect.width > 0 ? (focus.x - rect.left) / rect.width : 0.5;
  const ratioY = rect.height > 0 ? (focus.y - rect.top) / rect.height : 0.5;
  const focusX = currentCenter.x - currentViewWidth / 2 + ratioX * currentViewWidth;
  const focusY = currentCenter.y - currentViewHeight / 2 + ratioY * currentViewHeight;

  return {
    x: focusX - (ratioX - 0.5) * nextViewWidth,
    y: focusY - (ratioY - 0.5) * nextViewHeight,
  };
}

function renderLegend(pods) {
  const groupItems = uniqueSorted(pods.map((pod) => pod.group).filter(Boolean))
    .map((group) => legendItem(group, colorFor(`group:${group}`)));
  if (pods.some((pod) => !pod.group)) {
    groupItems.push(legendItem("Unlabeled group", unlabeledColor()));
  }

  const appItems = uniqueSorted(pods.map((pod) => pod.app).filter(Boolean))
    .map((app) => legendItem(app, colorFor(`app:${app}`)));
  if (pods.some((pod) => !pod.app)) {
    appItems.push(legendItem("Unlabeled app", unlabeledColor()));
  }

  legendEl.innerHTML = selectedGroup === "all"
    ? legendGroup("Application groups", groupItems)
    : legendGroup("Applications", appItems);
}

function legendGroup(title, items) {
  return `
    <div class="legend-group">
      <div class="legend-title">${escapeHTML(title)}</div>
      ${items.length ? items.join("") : '<p class="muted">No visible pods.</p>'}
    </div>
  `;
}

function legendItem(label, color) {
  return `
    <div class="legend-item">
      <span class="swatch" style="background:${escapeHTML(color)}"></span>
      <span>${escapeHTML(label)}</span>
    </div>
  `;
}

function detailRows(value) {
  const flat = flatten(value);
  const rows = Object.entries(flat);
  const primary = rows.filter(([key]) => !/^(labels|annotations|metrics)\./.test(key));
  const metadata = rows.filter(([key]) => /^(labels|annotations|metrics)\./.test(key));
  const renderRows = (items) => items.map(([key, val]) => `
      <div class="detail-row">
        <strong>${escapeHTML(formatDetailKey(key, flat))}</strong>
        <code>${escapeHTML(formatDetailValue(key, val, flat))}</code>
      </div>
    `).join("");
  return renderRows(primary) + (metadata.length
    ? `<details class="metadata-details"><summary>Labels & metrics <span>${metadata.length}</span></summary>${renderRows(metadata)}</details>`
    : "");
}

function formatDetailKey(key, row) {
  const metric = metricName(key);
  const unit = detailUnit(key, metric, row);
  return unit ? `${key} (${unit})` : key;
}

function formatDetailValue(key, value, row) {
  const metric = metricName(key);
  const numeric = Number(value);
  const isNumeric = value !== "" && value !== null && value !== undefined && Number.isFinite(numeric);

  if (metric === "cpu-usage" || key === "cpu") {
    return isNumeric ? formatMillicores(numeric) : String(value);
  }
  if (metric === "memory-usage" || key === "memory") {
    return isNumeric ? formatBytes(numeric) : String(value);
  }
  if (metric === "disk-throughput" || metric === "disk-bandwidth" || key === "disk") {
    return isNumeric ? formatRate(numeric) : String(value);
  }
  if (metric === "network-throughput" || metric === "network-bandwidth" || key === "network") {
    return isNumeric ? formatRate(numeric) : String(value);
  }
  if (metric?.startsWith("network-bandwidth.") || key === "bandwidth") {
    return isNumeric ? formatRate(numeric) : String(value);
  }
  if (metric?.startsWith("packet-loss.") || key === "packetLoss") {
    return isNumeric ? formatPacketLoss(numeric) : String(value);
  }
  if (metric?.startsWith("network-latency.") || (key === "value" && (row.kind === "latency" || row.kind === "network"))) {
    return isNumeric ? formatLatency(numeric) : String(value);
  }
  if (metric?.startsWith("rps.") || (key === "value" && row.kind === "rps")) {
    return isNumeric ? formatRPS(numeric) : String(value);
  }
  if (metric?.startsWith("traffic.") || (key === "value" && row.kind === "traffic")) {
    return isNumeric ? formatRate(numeric) : String(value);
  }
  if (key === "samples" && isNumeric) {
    return `${numeric} samples`;
  }
  return String(value);
}

function detailUnit(key, metric, row) {
  if (metric === "cpu-usage" || key === "cpu") return "mCPU";
  if (metric === "memory-usage" || key === "memory") return "bytes";
  if (metric === "disk-throughput" || metric === "disk-bandwidth" || key === "disk") return "B/s";
  if (metric === "network-throughput" || metric === "network-bandwidth" || key === "network") return "B/s";
  if (metric?.startsWith("network-bandwidth.") || key === "bandwidth") return "B/s";
  if (metric?.startsWith("packet-loss.") || key === "packetLoss") return "%";
  if (metric?.startsWith("network-latency.") || (key === "value" && (row.kind === "latency" || row.kind === "network"))) return "ms";
  if (metric?.startsWith("rps.") || (key === "value" && row.kind === "rps")) return "rps";
  if (metric?.startsWith("traffic.") || (key === "value" && row.kind === "traffic")) return "B/s";
  if (key === "samples") return "count";
  return "";
}

function metricName(key) {
  if (key.startsWith("metrics.")) return key.slice("metrics.".length);
  if (key.startsWith("annotations.")) return key.slice("annotations.".length);
  const knownNames = [
    "cpu-usage",
    "memory-usage",
    "disk-throughput",
    "network-throughput",
    "disk-bandwidth",
    "network-bandwidth",
    "network-latency.",
    "packet-loss.",
    "rps.",
    "traffic.",
  ];
  const match = knownNames.find((name) => key.includes(name));
  if (!match) return null;
  return key.slice(key.indexOf(match));
}

function flatten(value, prefix = "", out = {}) {
  if (!value || typeof value !== "object") return out;
  for (const [key, val] of Object.entries(value)) {
    const next = prefix ? `${prefix}.${key}` : key;
    if (val && typeof val === "object" && !Array.isArray(val)) {
      flatten(val, next, out);
    } else if (Array.isArray(val)) {
      out[next] = val.join(", ");
    } else {
      out[next] = val ?? "";
    }
  }
  return out;
}

function groupBy(items, fn) {
  const out = new Map();
  for (const item of items) {
    const key = fn(item) || "";
    if (!out.has(key)) out.set(key, []);
    out.get(key).push(item);
  }
  return out;
}

function countGroups(pods) {
  return new Set(pods.map((pod) => pod.group).filter(Boolean)).size;
}

function podKey(pod) {
  return `${pod.namespace}/${pod.name}`;
}

function nodeLabelLines(name) {
  if (name.length <= 18) return [name];
  const parts = name.split("-");
  if (parts.length < 4) return [name];
  const splitIndex = /^\d+$/.test(parts[parts.length - 1]) ? parts.length - 2 : parts.length - 1;
  return [parts.slice(0, splitIndex).join("-"), parts.slice(splitIndex).join("-")];
}

function colorFor(value) {
  let hash = 0;
  for (const char of value || "default") hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  return groupColors[hash % groupColors.length];
}

function podColor(pod) {
  if (selectedGroup !== "all") {
    return pod.app ? colorFor(`app:${pod.app}`) : unlabeledColor();
  }
  return pod.group ? colorFor(`group:${pod.group}`) : unlabeledColor();
}

function unlabeledColor() {
  return "#98a2b3";
}

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b));
}

function clamp(value, min, max) {
  if (min > max) return (min + max) / 2;
  return Math.max(min, Math.min(max, value));
}

function midpoint(a, b) {
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}

function curvedPath(a, b, curve) {
  const mid = midpoint(a, b);
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.max(1, Math.hypot(dx, dy));
  const cx = mid.x - (dy / len) * curve;
  const cy = mid.y + (dx / len) * curve;
  return `M ${a.x} ${a.y} Q ${cx} ${cy} ${b.x} ${b.y}`;
}

function curvePoint(a, b, curve) {
  const mid = midpoint(a, b);
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.max(1, Math.hypot(dx, dy));
  return {
    x: mid.x - (dy / len) * curve * 0.5,
    y: mid.y + (dx / len) * curve * 0.5,
  };
}

function svg(name, attrs) {
  const el = document.createElementNS("http://www.w3.org/2000/svg", name);
  for (const [key, value] of Object.entries(attrs || {})) {
    el.setAttribute(key, value);
  }
  return el;
}

function svgText(x, y, text, className, anchor = "middle") {
  const el = svg("text", { x, y, class: className, "text-anchor": anchor });
  el.textContent = text;
  return el;
}

function clear(el) {
  while (el.firstChild) el.removeChild(el.firstChild);
}

function pill(value, label) {
  return `<span class="pill"><strong>${escapeHTML(value)}</strong><small>${escapeHTML(label)}</small></span>`;
}

function formatMillicores(value) {
  return Number.isFinite(value) && value >= 0 ? `${value.toFixed(1)} mCPU` : "n/a";
}

function formatBytes(value) {
  if (!Number.isFinite(value) || value < 0) return "n/a";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let next = value;
  let index = 0;
  while (next >= 1024 && index < units.length - 1) {
    next /= 1024;
    index += 1;
  }
  return `${next.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function formatRate(value) {
  return Number.isFinite(value) && value >= 0 ? `${formatBytes(value)}/s` : "n/a";
}

function formatLatency(value) {
  return Number.isFinite(value) ? `${value.toFixed(3)} ms` : "n/a";
}

function formatPacketLoss(value) {
  return Number.isFinite(value) ? `${value.toFixed(2)}%` : "n/a";
}

function formatRPS(value) {
  return Number.isFinite(value) ? `${value.toFixed(2)} rps` : "n/a";
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}
