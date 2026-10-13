// Glassbound — Originkit. Shader supplied by the user.
export const VERT = `#version 300 es
in vec2 aPos;
void main() { gl_Position = vec4(aPos, 0.0, 1.0); }
`;

export const FRAG = `#version 300 es
precision highp float;
out vec4 fragColor;
uniform vec2 uRes;
uniform vec3 uCamPos;
uniform vec3 uCamR;
uniform vec3 uCamU;
uniform vec3 uCamF;
uniform float uFocal;
uniform float uOff;
uniform vec2 uGrid;
uniform vec2 uBallQ;
uniform vec2 uBall;
uniform float uMaxH;
uniform vec2 uMound;
uniform vec3 uTileP;
uniform vec3 uGaps;
uniform vec3 uHalf;
uniform vec3 uL;
uniform vec3 uAccent;
uniform vec3 uPeach;
uniform vec3 uInner;
uniform vec3 uFloor;
uniform vec3 uStudC;
uniform vec3 uBg;
uniform vec4 uLook;
uniform vec3 uGlass;
uniform vec3 uPtr;
uniform vec3 uFx;

const float FT = 0.02;
float gH;
float gMat;

float hash21(vec2 p) {
    p = mod(p, 256.0);
    vec3 p3 = fract(vec3(p.xyx) * 0.1031);
    p3 += dot(p3, p3.yzx + 33.33);
    return fract((p3.x + p3.y) * p3.z);
}

vec2 toQ(vec2 xz) {
    return vec2(dot(xz, vec2(uGrid.x, -uGrid.y)), dot(xz, vec2(uGrid.y, uGrid.x))) + uBallQ;
}

float cellH(vec2 id) {
    vec2 c = id + 0.5;
    float hv = hash21(id);
    float m = 1.0 - smoothstep(uMound.y, uMound.x, length(c - uBallQ));
    float keep = step(uTileP.y, hash21(id + 91.7));
    m *= mix(keep, 1.0, step(0.6, m));
    float j = 1.0 + uTileP.x * (hv * 2.0 - 1.0);
    float pm = uPtr.z * (1.0 - smoothstep(0.3, 2.2, length(c - uPtr.xy))) * 0.75;
    return uMaxH * max(m * j, pm * (0.8 + 0.4 * hv));
}

float tileSD(vec2 q, float y, vec2 id, out float h) {
    h = cellH(id);
    float top = FT + h;
    float gap = mix(uGaps.y, uGaps.x, smoothstep(0.0, 0.12 * uMaxH + 0.001, h));
    float hs = 0.5 - 0.5 * gap;
    float r = min(uTileP.z, hs);
    vec2 lp = q - id - 0.5;
    vec2 dq = abs(lp) - vec2(hs - r);
    float d2 = length(max(dq, 0.0)) + min(max(dq.x, dq.y), 0.0) - r;
    float hh = 0.5 * top;
    float bev = min(0.025, hh * 0.8);
    vec2 w = vec2(d2 + bev, abs(y - hh) - hh + bev);
    return min(max(w.x, w.y), 0.0) + length(max(w, 0.0)) - bev;
}

float mapF(vec3 p) {
    vec2 q = toQ(p.xz);
    vec2 id = floor(q);
    vec2 f = q - id;
    vec2 o = vec2(f.x < 0.5 ? -1.0 : 1.0, f.y < 0.5 ? -1.0 : 1.0);
    float h0; float h1; float h2; float h3;
    float d0 = tileSD(q, p.y, id, h0);
    float d1 = tileSD(q, p.y, id + vec2(o.x, 0.0), h1);
    float d2 = tileSD(q, p.y, id + vec2(0.0, o.y), h2);
    float d3 = tileSD(q, p.y, id + o, h3);
    float d = d0; gH = h0; gMat = 1.0;
    if (d1 < d) { d = d1; gH = h1; }
    if (d2 < d) { d = d2; gH = h2; }
    if (d3 < d) { d = d3; gH = h3; }
    if (p.y < d) { d = p.y; gMat = 0.0; }
    vec2 cq = q - floor(q + 0.5);
    float ds = length(vec2((abs(cq.x) + abs(cq.y)) * 0.7071, p.y)) - uGaps.z;
    if (ds < d) { d = ds; gMat = 2.0; }
    return min(d, 0.45);
}

vec3 nrmF(vec3 p) {
    const vec2 k = vec2(1.0, -1.0);
    float e = 0.002;
    return normalize(k.xyy * mapF(p + k.xyy * e) + k.yyx * mapF(p + k.yyx * e) + k.yxy * mapF(p + k.yxy * e) + k.xxx * mapF(p + k.xxx * e));
}

float topY() { return FT + uMaxH * 1.3 + 0.05; }

bool marchF(vec3 ro, vec3 rd, int steps, out vec3 hp) {
    hp = ro;
    if (rd.y > -0.002) return false;
    float t = 0.0;
    float Y = topY();
    if (ro.y > Y) t = (ro.y - Y) / (-rd.y);
    for (int i = 0; i < 96; i++) {
        if (i >= steps) break;
        vec3 p = ro + rd * t;
        float d = mapF(p);
        if (d < 0.0015) { hp = p; return true; }
        t += d * 0.9;
        if (p.y < -0.05) break;
    }
    hp = ro + rd * t;
    return true;
}

float shadowF(vec3 ro) {
    float res = 1.0;
    float t = 0.03;
    float Y = topY();
    for (int i = 0; i < 18; i++) {
        vec3 p = ro + uL * t;
        if (p.y > Y) break;
        float h = mapF(p);
        res = min(res, uLook.y * h / t);
        if (res < 0.02) break;
        t += clamp(h, 0.03, 0.25);
    }
    return clamp(res, 0.0, 1.0);
}

float sphShadow(vec3 ro, vec3 rd, vec3 c, float r, float k) {
    vec3 oc = ro - c;
    float b = dot(oc, rd);
    float cc = dot(oc, oc) - r * r;
    float h = b * b - cc;
    float d = sqrt(max(0.0, r * r - h)) - r;
    float t = -b - sqrt(max(h, 0.0));
    return (t < 0.0) ? 1.0 : smoothstep(0.0, 1.0, 2.5 * k * d / t);
}

float sphOcc(vec3 pos, vec3 nor, vec3 c, float r) {
    vec3 di = c - pos;
    float l = length(di);
    return 1.0 - max(0.0, dot(nor, di / l)) * (r * r) / (l * l);
}

float aoF(vec3 p, vec3 n) {
    float o = 0.0;
    float s = 1.0;
    for (int i = 1; i <= 3; i++) {
        float h = 0.04 * float(i);
        float d = mapF(p + n * h);
        o += (h - d) * s;
        s *= 0.6;
    }
    return clamp(1.0 - 3.5 * o, 0.0, 1.0);
}

vec3 shadeFloor(vec3 p, vec3 rd, bool full) {
    mapF(p);
    float mat = gMat;
    float h = gH;
    vec3 n = nrmF(p);
    vec3 C = vec3(0.0, uBall.y, 0.0);
    float R = uBall.x;
    float tn = clamp(h / max(uMaxH, 1e-3), 0.0, 1.0);
    vec3 alb;
    if (mat < 0.5) {
        alb = mix(uBg, uFloor, 0.4) * 0.6;
    } else if (mat < 1.5) {
        alb = mix(uFloor, uPeach, smoothstep(0.04, 0.3, tn));
        alb = mix(alb, uAccent, smoothstep(0.38, 0.62, tn));
    } else {
        alb = uStudC;
    }
    vec3 sp = p + n * 0.004;
    float sh = full ? shadowF(sp) : 1.0;
    sh *= mix(1.0, sphShadow(sp, uL, C, R, uLook.y * 0.4), 0.85);
    float ao = (full ? aoF(p, n) : 1.0) * sphOcc(p, n, C, R);
    float dif = max(dot(n, uL), 0.0);
    float sl = mix(1.0, sh, uLook.z);
    vec3 amb = vec3(uLook.x * (0.62 + 0.38 * n.y) * ao);
    amb *= mix(vec3(1.0), uAccent * 1.1 + 0.1, 0.25 * (1.0 - sl));
    vec3 col = alb * (amb + (1.0 - uLook.x) * 1.3 * dif * sl);
    col += 0.06 * pow(max(dot(reflect(rd, n), uL), 0.0), 18.0) * sl * step(0.5, mat);
    return col;
}

vec3 env(vec3 r) {
    float up = smoothstep(-0.3, 0.8, r.y);
    vec3 c = mix(uFloor * 0.9 + uBg * 0.1, vec3(0.62, 0.6, 0.58), up);
    c += vec3(1.0) * pow(max(dot(r, uL), 0.0), 24.0) * 0.9;
    return c;
}

float hash12(vec2 p) {
    vec3 p3 = fract(vec3(p.xyx) * 0.1031);
    p3 += dot(p3, p3.yzx + 33.33);
    return fract((p3.x + p3.y) * p3.z);
}

void main() {
    vec2 frag = gl_FragCoord.xy;
    vec2 uv = (frag - 0.5 * uRes) / uRes.y;
    vec3 rd = normalize(uCamR * uv.x + uCamU * (uv.y - uOff) + uCamF * uFocal);
    vec3 ro = uCamPos;
    vec3 C = vec3(0.0, uBall.y, 0.0);
    float R = uBall.x;
    vec3 col;

    vec3 oc = ro - C;
    float b = dot(oc, rd);
    float c = dot(oc, oc) - R * R;
    float h = b * b - c;
    if (h > 0.0 && -b - sqrt(h) > 0.0) {
        float t = -b - sqrt(h);
        vec3 P = ro + rd * t;
        vec3 N = (P - C) / R;
        float side = dot(N, uHalf);
        float cosv = max(dot(-rd, N), 0.0);
        float F = 0.04 + 0.96 * pow(1.0 - cosv, 5.0);
        vec3 refl = reflect(rd, N);
        vec3 base;
        if (side > 0.0) {
            float dif = max(dot(N, uL), 0.0);
            float amb = uLook.x * (0.5 + 0.5 * N.y);
            base = uAccent * (amb + (1.0 - uLook.x) * 1.3 * dif);
            base = mix(base, base * vec3(0.9, 0.8, 0.7), smoothstep(0.2, -0.6, dot(N, uL)));
        } else {
            float eta = 1.0 / (1.0 + 0.5 * uGlass.z);
            vec3 r2 = refract(rd, N, eta);
            vec3 pc = P - C;
            float b2 = dot(pc, r2);
            float c2 = dot(pc, pc) - R * R;
            float h2 = b2 * b2 - c2;
            float tex = -b2 + sqrt(max(h2, 0.0));
            float dn = dot(r2, uHalf);
            float tp = (abs(dn) > 1e-4) ? dot(C - P, uHalf) / dn : -1.0;
            if (tp > 0.0 && tp < tex) {
                vec3 Q = P + r2 * tp;
                float rr = clamp(length(Q - C) / R, 0.0, 1.0);
                vec3 inner = mix(uInner * 1.15, uInner * 0.75, smoothstep(0.1, 0.95, rr));
                float lit = 0.8 + 0.3 * max(dot(-uHalf, uL), 0.0);
                base = inner * lit;
                base = mix(base, uAccent * 0.95, smoothstep(0.9, 0.99, rr));
            } else {
                vec3 E = P + r2 * tex;
                vec3 NE = (E - C) / R;
                vec3 r3 = refract(r2, -NE, 1.0 / eta);
                if (dot(r3, r3) < 1e-4) r3 = reflect(r2, -NE);
                vec3 hp;
                marchF(E, r3, 48, hp);
                base = shadeFloor(hp, r3, false) * vec3(0.94, 0.93, 0.91);
            }
        }
        col = mix(base, env(refl), clamp(F * (0.6 + uGlass.x * 0.8), 0.0, 1.0));
        float rim = smoothstep(0.55, 0.95, 1.0 - cosv);
        col += vec3(0.92, 0.9, 0.88) * rim * rim * uGlass.x * (side > 0.0 ? 0.25 : 0.6);
        vec3 L2 = normalize(vec3(0.75, 0.45, -0.5));
        float s = pow(max(dot(refl, uL), 0.0), 420.0) * 2.5 + pow(max(dot(refl, L2), 0.0), 900.0) * 1.6;
        col += vec3(s) * uGlass.y;
    } else {
        vec3 hp;
        marchF(ro, rd, 80, hp);
        col = shadeFloor(hp, rd, true);
    }

    float x01 = frag.x / uRes.x;
    float y01 = 1.0 - frag.y / uRes.y;
    float bandF = smoothstep(0.16, 0.45, abs(y01 - (0.35 + 0.27 * x01)));
    col *= 1.0 - uLook.w * bandF;

    float vig = smoothstep(0.3, 1.0, length(uv));
    col *= 1.0 - uFx.y * vig;
    float g = hash12(frag + fract(uFx.z * 7.31) * 113.0) - 0.5;
    col += g * uFx.x * 0.25;

    fragColor = vec4(clamp(col, 0.0, 1.0), 1.0);
}
`;

export const UNIFORMS = [
  "uRes",
  "uCamPos",
  "uCamR",
  "uCamU",
  "uCamF",
  "uFocal",
  "uOff",
  "uGrid",
  "uBallQ",
  "uBall",
  "uMaxH",
  "uMound",
  "uTileP",
  "uGaps",
  "uHalf",
  "uL",
  "uAccent",
  "uPeach",
  "uInner",
  "uFloor",
  "uStudC",
  "uBg",
  "uLook",
  "uGlass",
  "uPtr",
  "uFx",
] as const;
