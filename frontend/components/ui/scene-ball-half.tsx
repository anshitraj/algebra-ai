"use client";

// Glassbound — Originkit. Adapted from the component supplied by the user.
import type { CSSProperties } from "react";
import { useEffect, useRef, useState } from "react";
import { FRAG, UNIFORMS, VERT } from "./glassbound-shaders";
import styles from "./scene-ball-half.module.css";

const ADVANCED_DEFAULTS = {
  tilt: 50,
  perspective: 80,
  ballOffset: 9,
  gridAngle: 20,
  travelAngle: 10,
  ballRadius: 87,
  moundInner: 35,
  heightJitter: 20,
  missingTiles: 25,
  tileGap: 8,
  floorGap: 3,
  cornerRound: 12,
  studSize: 4,
  floorLift: 16,
  peachColor: "#F0D6C9",
  innerColor: "#A8442A",
  studColor: "#8E8882",
  lightAngle: 135,
  lightHeight: 50,
  ambient: 65,
  shadowEdge: 10,
  shadowStrength: 80,
  band: 30,
  glassRim: 60,
  specular: 80,
  refraction: 40,
  quality: 60,
  grain: 0,
  vignette: 0,
  hover: 50,
};
const TILES_PER_SECOND = 43.4 / 15.6;
const FT = 0.02;
type V3 = [number, number, number];
const cross = (a: V3, b: V3): V3 => [
  a[1] * b[2] - a[2] * b[1],
  a[2] * b[0] - a[0] * b[2],
  a[0] * b[1] - a[1] * b[0],
];
const norm = (a: V3): V3 => {
  const length = Math.hypot(...a) || 1;
  return [a[0] / length, a[1] / length, a[2] / length];
};

function parseColor(input: string): V3 {
  const s = input.trim();
  if (s.startsWith("#")) {
    let h = s.slice(1);
    if (h.length === 3 || h.length === 4)
      h = h
        .split("")
        .map((c) => c + c)
        .join("");
    const n = parseInt(h.slice(0, 6), 16);
    return Number.isNaN(n)
      ? [0, 0, 0]
      : [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255];
  }
  const match = s.match(/rgba?\(([^)]+)\)/i);
  if (!match) return [0, 0, 0];
  const parts = match[1]
    .split(/[\s,\/]+/)
    .filter(Boolean)
    .map(Number.parseFloat);
  return [(parts[0] || 0) / 255, (parts[1] || 0) / 255, (parts[2] || 0) / 255];
}

function compile(gl: WebGL2RenderingContext, type: number, source: string) {
  const shader = gl.createShader(type);
  if (!shader) throw new Error("Shader creation failed");
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
    const log = gl.getShaderInfoLog(shader);
    gl.deleteShader(shader);
    throw new Error(log || "Shader compilation failed");
  }
  return shader;
}

function createProgram(gl: WebGL2RenderingContext) {
  const vertex = compile(gl, gl.VERTEX_SHADER, VERT);
  let fragment: WebGLShader | null = null;
  let program: WebGLProgram | null = null;
  try {
    fragment = compile(gl, gl.FRAGMENT_SHADER, FRAG);
    program = gl.createProgram();
    if (!program) throw new Error("Program creation failed");
    gl.attachShader(program, vertex);
    gl.attachShader(program, fragment);
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS))
      throw new Error(gl.getProgramInfoLog(program) || "Shader linking failed");
    return program;
  } catch (error) {
    if (program) gl.deleteProgram(program);
    throw error;
  } finally {
    gl.deleteShader(vertex);
    if (fragment) gl.deleteShader(fragment);
  }
}

export interface SceneBallHalfProps {
  background?: string;
  accent?: string;
  speed?: number;
  size?: number;
  radius?: number;
  lift?: number;
  advanced?: Partial<typeof ADVANCED_DEFAULTS>;
  paused?: boolean;
  className?: string;
  style?: CSSProperties;
}

type Config = Required<
  Pick<
    SceneBallHalfProps,
    "background" | "accent" | "speed" | "size" | "radius" | "lift" | "paused"
  >
> & { advanced: typeof ADVANCED_DEFAULTS };

export default function SceneBallHalf({
  speed = 50,
  size = 125,
  accent = "#FF0000",
  background = "#000000",
  radius = 3,
  lift = 100,
  advanced,
  paused = false,
  className,
  style,
}: SceneBallHalfProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const propsRef = useRef<Config>({
    speed,
    size,
    accent,
    background,
    radius,
    lift,
    paused,
    advanced: { ...ADVANCED_DEFAULTS, ...advanced },
  });
  const ptrRef = useRef({ x: 0, y: 0, over: 0, down: 0 });
  const invalidateRef = useRef<(() => void) | null>(null);
  const [contextVersion, setContextVersion] = useState(0);

  useEffect(() => {
    propsRef.current = {
      speed,
      size,
      accent,
      background,
      radius,
      lift,
      paused,
      advanced: { ...ADVANCED_DEFAULTS, ...advanced },
    };
    invalidateRef.current?.();
  }, [speed, size, accent, background, radius, lift, paused, advanced]);

  useEffect(() => {
    const root = rootRef.current;
    const canvas = canvasRef.current;
    if (!root || !canvas) return;
    const gl = canvas.getContext("webgl2", {
      antialias: false,
      premultipliedAlpha: false,
      // Static and paused frames must survive compositing and viewport changes.
      // The framebuffer is capped below to keep this extra buffer small.
      preserveDrawingBuffer: true,
    });
    if (!gl) {
      root.dataset.renderer = "fallback";
      return;
    }
    let program: WebGLProgram;
    try {
      program = createProgram(gl);
    } catch {
      root.dataset.renderer = "fallback";
      return;
    }

    const U = Object.fromEntries(
      UNIFORMS.map((name) => [name, gl.getUniformLocation(program, name)]),
    ) as Record<(typeof UNIFORMS)[number], WebGLUniformLocation | null>;
    const buffer = gl.createBuffer();
    const vao = gl.createVertexArray();
    if (!buffer || !vao) {
      gl.deleteBuffer(buffer);
      gl.deleteVertexArray(vao);
      gl.deleteProgram(program);
      root.dataset.renderer = "fallback";
      return;
    }
    gl.bindVertexArray(vao);
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
    gl.bufferData(
      gl.ARRAY_BUFFER,
      new Float32Array([-1, -1, 3, -1, -1, 3]),
      gl.STATIC_DRAW,
    );
    const position = gl.getAttribLocation(program, "aPos");
    gl.enableVertexAttribArray(position);
    gl.vertexAttribPointer(position, 2, gl.FLOAT, false, 0, 0);

    let cssW = root.clientWidth || 1;
    let cssH = root.clientHeight || 1;
    let inView = false;
    let lost = false;
    let disposed = false;
    let raf = 0;
    let last = -1;
    let lastDraw = -Infinity;
    let dist = 0;
    let clock = 0;
    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    const eased = { over: 0, down: 0, px: 0, py: 0 };
    const canDraw = () =>
      !disposed && !lost && inView && document.visibilityState === "visible";
    const canRun = () =>
      canDraw() &&
      !media.matches &&
      !propsRef.current.paused &&
      propsRef.current.speed > 0;

    function running(active: boolean) {
      const value = String(active);
      if (root!.dataset.running !== value) root!.dataset.running = value;
    }
    function stop() {
      cancelAnimationFrame(raf);
      raf = 0;
      last = -1;
      running(false);
    }
    function invalidate() {
      stop();
      lastDraw = -Infinity;
      if (canDraw()) raf = requestAnimationFrame(frame);
    }
    invalidateRef.current = invalidate;

    function frame(now: number) {
      raf = 0;
      if (!canDraw()) {
        stop();
        return;
      }
      if (now - lastDraw < 1000 / 30) {
        raf = requestAnimationFrame(frame);
        return;
      }
      lastDraw = now;
      const dt =
        last < 0 || !canRun() ? 0 : Math.min((now - last) / 1000, 1 / 20);
      last = now;
      clock += dt;
      const p = propsRef.current;
      const adv = p.advanced;
      // Bound the ray-marched framebuffer independently of the CSS size.
      const dpr = Math.min(window.devicePixelRatio || 1, 1.5);
      const requestedQuality = Math.max(0.2, Math.min(1, adv.quality / 100));
      const q = Math.min(
        requestedQuality,
        Math.sqrt(180_000 / (cssW * cssH)) / dpr,
      );
      const W = Math.max(1, Math.round(cssW * dpr * q));
      const H = Math.max(1, Math.round(cssH * dpr * q));
      if (canvas!.width !== W || canvas!.height !== H) {
        canvas!.width = W;
        canvas!.height = H;
      }

      const ptr = ptrRef.current;
      const k = 1 - Math.exp(-dt * 6);
      eased.over += (ptr.over - eased.over) * k;
      eased.down += (ptr.down - eased.down) * k;
      const tx = ptr.over ? (ptr.x / cssW) * 2 - 1 : 0;
      const ty = ptr.over ? 1 - (ptr.y / cssH) * 2 : 0;
      eased.px += (tx - eased.px) * k;
      eased.py += (ty - eased.py) * k;
      const hk = Math.max(0, adv.hover) / 50;
      const rate = Math.max(0, Math.min(100, p.speed)) / 50;
      dist += TILES_PER_SECOND * rate * dt;
      const R = Math.max(0.2, adv.ballRadius / 100);
      const maxH = (Math.max(0, Math.min(100, p.lift)) / 100) * 0.5;
      const jitter = adv.heightJitter / 100;
      const ballY = FT + maxH * (1 + jitter) + R;
      const ta = (adv.travelAngle * Math.PI) / 180;
      const ga = (adv.gridAngle * Math.PI) / 180;
      const cg = Math.cos(ga),
        sg = Math.sin(ga);
      const Bx = -Math.cos(ta) * dist,
        Bz = Math.sin(ta) * dist;
      const wrap = (v: number) => ((v % 256) + 256) % 256;
      const bq: [number, number] = [
        wrap(Bx * cg - Bz * sg),
        wrap(Bx * sg + Bz * cg),
      ];
      const axis: V3 = [Math.sin(ta), 0, Math.cos(ta)];
      const theta = (dist / R) % (Math.PI * 2);
      const elBase = (adv.tilt * Math.PI) / 180;
      const n0: V3 = [0, -Math.sin(elBase), Math.cos(elBase)];
      const ct = Math.cos(theta),
        st = Math.sin(theta);
      const kxv = cross(axis, n0);
      const kd = axis[0] * n0[0] + axis[1] * n0[1] + axis[2] * n0[2];
      const half: V3 = [
        n0[0] * ct + kxv[0] * st + axis[0] * kd * (1 - ct),
        n0[1] * ct + kxv[1] * st + axis[1] * kd * (1 - ct),
        n0[2] * ct + kxv[2] * st + axis[2] * kd * (1 - ct),
      ];
      const focal = Math.max(0.5, adv.perspective / 100);
      const el = elBase + ((eased.py * 4 * Math.PI) / 180) * hk * eased.over;
      const yaw = ((eased.px * 6 * Math.PI) / 180) * hk * eased.over;
      const ballSize = Math.min(p.size, cssW * 0.6, cssH * 0.65);
      const D = (R * focal * cssH) / Math.max(10, ballSize / 2);
      const back: V3 = [
        -Math.sin(yaw) * Math.cos(el),
        Math.sin(el),
        -Math.cos(yaw) * Math.cos(el),
      ];
      const camPos: V3 = [back[0] * D, ballY + back[1] * D, back[2] * D];
      const fwd: V3 = [-back[0], -back[1], -back[2]];
      const right = norm([fwd[2], 0, -fwd[0]]);
      const up = cross(fwd, right);
      const off = adv.ballOffset / 100;
      let ptrQ: [number, number] = [9999, 9999],
        ptrS = 0;
      if (eased.over > 0.001 && hk > 0) {
        const ux = (ptr.x - cssW / 2) / cssH,
          uy = (cssH / 2 - ptr.y) / cssH;
        const dir: V3 = [
          right[0] * ux + up[0] * (uy - off) + fwd[0] * focal,
          right[1] * ux + up[1] * (uy - off) + fwd[1] * focal,
          right[2] * ux + up[2] * (uy - off) + fwd[2] * focal,
        ];
        if (dir[1] < -1e-4) {
          const t = (FT + maxH * 0.5 - camPos[1]) / dir[1];
          const X = camPos[0] + dir[0] * t,
            Z = camPos[2] + dir[2] * t;
          ptrQ = [X * cg - Z * sg + bq[0], X * sg + Z * cg + bq[1]];
          ptrS = Math.min(1, eased.over * hk * (0.6 + 0.4 * eased.down));
        }
      }
      const la = (adv.lightAngle * Math.PI) / 180,
        lh = (adv.lightHeight * Math.PI) / 180;
      const L = norm([
        Math.cos(lh) * Math.cos(la),
        Math.sin(lh),
        Math.cos(lh) * Math.sin(la),
      ]);
      const bg = parseColor(p.background),
        fl = adv.floorLift / 100;
      const floor: V3 = [
        bg[0] + (1 - bg[0]) * fl,
        bg[1] + (1 - bg[1]) * fl,
        bg[2] + (1 - bg[2]) * fl,
      ];
      const rad = Math.max(1, p.radius);

      gl!.viewport(0, 0, W, H);
      gl!.useProgram(program);
      gl!.bindVertexArray(vao);
      gl!.uniform2f(U.uRes, W, H);
      gl!.uniform3fv(U.uCamPos, camPos);
      gl!.uniform3fv(U.uCamR, right);
      gl!.uniform3fv(U.uCamU, up);
      gl!.uniform3fv(U.uCamF, fwd);
      gl!.uniform1f(U.uFocal, focal);
      gl!.uniform1f(U.uOff, off);
      gl!.uniform2f(U.uGrid, cg, sg);
      gl!.uniform2fv(U.uBallQ, bq);
      gl!.uniform2f(U.uBall, R, ballY);
      gl!.uniform1f(U.uMaxH, maxH);
      gl!.uniform2f(
        U.uMound,
        rad,
        rad * Math.max(0.05, Math.min(0.95, adv.moundInner / 100)),
      );
      gl!.uniform3f(
        U.uTileP,
        jitter,
        adv.missingTiles / 100,
        adv.cornerRound / 100,
      );
      gl!.uniform3f(
        U.uGaps,
        adv.tileGap / 100,
        adv.floorGap / 100,
        adv.studSize / 100,
      );
      gl!.uniform3fv(U.uHalf, half);
      gl!.uniform3fv(U.uL, L);
      gl!.uniform3fv(U.uAccent, parseColor(p.accent));
      gl!.uniform3fv(U.uPeach, parseColor(adv.peachColor));
      gl!.uniform3fv(U.uInner, parseColor(adv.innerColor));
      gl!.uniform3fv(U.uFloor, floor);
      gl!.uniform3fv(U.uStudC, parseColor(adv.studColor));
      gl!.uniform3fv(U.uBg, bg);
      gl!.uniform4f(
        U.uLook,
        adv.ambient / 100,
        Math.max(1, adv.shadowEdge),
        adv.shadowStrength / 100,
        adv.band / 100,
      );
      gl!.uniform3f(
        U.uGlass,
        adv.glassRim / 100,
        adv.specular / 100,
        adv.refraction / 100,
      );
      gl!.uniform3f(U.uPtr, ptrQ[0], ptrQ[1], ptrS);
      gl!.uniform3f(U.uFx, adv.grain / 100, adv.vignette / 100, clock);
      gl!.drawArrays(gl!.TRIANGLES, 0, 3);
      if (root!.dataset.renderer !== "webgl") root!.dataset.renderer = "webgl";
      if (canRun()) {
        running(true);
        raf = requestAnimationFrame(frame);
      } else stop();
    }

    const resizeObserver = new ResizeObserver(([entry]) => {
      cssW = Math.max(1, entry.contentRect.width);
      cssH = Math.max(1, entry.contentRect.height);
      invalidate();
    });
    resizeObserver.observe(root);
    const observer = new IntersectionObserver(([entry]) => {
      inView = entry.isIntersecting;
      invalidate();
    });
    observer.observe(root);
    const onUp = () => {
      ptrRef.current.down = 0;
    };
    const onLost = (event: Event) => {
      event.preventDefault();
      lost = true;
      stop();
      root.dataset.renderer = "fallback";
    };
    const onRestore = () => setContextVersion((v) => v + 1);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onUp);
    document.addEventListener("visibilitychange", invalidate);
    media.addEventListener("change", invalidate);
    canvas.addEventListener("webglcontextlost", onLost);
    canvas.addEventListener("webglcontextrestored", onRestore);

    return () => {
      disposed = true;
      stop();
      invalidateRef.current = null;
      resizeObserver.disconnect();
      observer.disconnect();
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
      document.removeEventListener("visibilitychange", invalidate);
      media.removeEventListener("change", invalidate);
      canvas.removeEventListener("webglcontextlost", onLost);
      canvas.removeEventListener("webglcontextrestored", onRestore);
      gl.deleteBuffer(buffer);
      gl.deleteVertexArray(vao);
      gl.deleteProgram(program);
    };
  }, [contextVersion]);

  return (
    <div
      ref={rootRef}
      className={`${styles.root} ${className ?? ""}`}
      data-glassbound=""
      onPointerEnter={(e) => {
        ptrRef.current.over = 1;
        ptrRef.current.x = e.nativeEvent.offsetX;
        ptrRef.current.y = e.nativeEvent.offsetY;
      }}
      onPointerMove={(e) => {
        ptrRef.current.over = 1;
        ptrRef.current.x = e.nativeEvent.offsetX;
        ptrRef.current.y = e.nativeEvent.offsetY;
      }}
      onPointerLeave={() => {
        ptrRef.current.over = 0;
      }}
      onPointerDown={() => {
        ptrRef.current.down = 1;
      }}
      style={{
        ...style,
        position: "relative",
        width: "100%",
        height: "100%",
        minWidth: 0,
        minHeight: 0,
        overflow: "hidden",
        background,
      }}
    >
      <canvas
        ref={canvasRef}
        aria-hidden="true"
        style={{
          display: "block",
          width: "100%",
          height: "100%",
          touchAction: "pan-y",
          opacity: "var(--glassbound-canvas-opacity, 0)",
        }}
      />
    </div>
  );
}
