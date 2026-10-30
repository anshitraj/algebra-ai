"use client";

import dynamic from "next/dynamic";
import { useCallback, useState, type CSSProperties } from "react";
import { useReducedMotion } from "motion/react";
import styles from "./glassbound-accent.module.css";

// The decorative preview is server rendered. Download and compile the shader
// only after someone chooses to explore it, never in the scroll path.
const Scene = dynamic(() => import("@/components/ui/scene-ball-half"), {
  ssr: false,
});
const ADVANCED = {
  peachColor: "#e4e7d8",
  innerColor: "#465338",
  studColor: "#a2a698",
  floorLift: 3,
  ambient: 76,
  shadowStrength: 62,
  band: 10,
  ballOffset: 6,
  glassRim: 68,
  specular: 72,
  refraction: 44,
  quality: 55,
  missingTiles: 18,
  hover: 38,
};
const TILES = Array.from({ length: 25 }, (_, i) => {
  const x = (i % 5) - 2;
  const y = Math.floor(i / 5) - 2;
  const height = Math.max(0, 1 - Math.hypot(x, y) / 3);
  return {
    "--tile-height": `${height * 16}px`,
    "--tile-tone": `${Math.round(28 + height * 40)}%`,
  } as CSSProperties;
});

export function GlassboundAccent() {
  const [interactive, setInteractive] = useState(false);
  const [ready, setReady] = useState(false);
  const [paused, setPaused] = useState(false);
  const reducedMotion = useReducedMotion();
  const onReady = useCallback(() => setReady(true), []);
  const state = !interactive ? "preview" : ready ? "interactive" : "loading";

  return (
    <figure
      className={styles.figure}
      data-state={state}
      aria-label="An olive glass sphere above a tiled landscape"
    >
      <div className={styles.fallback} aria-hidden="true">
        <span className={styles.fallbackGrid} />
        <span className={styles.terrain}>
          {TILES.map((style, i) => (
            <span key={i} style={style} />
          ))}
        </span>
        <span className={styles.ballShadow} />
        <span className={styles.fallbackBall} />
        <span className={styles.orbit} />
      </div>
      {interactive && (
        <div className={styles.stage}>
          <Scene
            background="#f8f8f3"
            accent="#86956b"
            speed={24}
            size={165}
            radius={3.4}
            lift={72}
            advanced={ADVANCED}
            paused={paused || Boolean(reducedMotion)}
            onReady={onReady}
          />
        </div>
      )}
      <figcaption className={styles.caption}>
        <span>
          GLASSBOUND <i>/</i> ORIGINKIT
        </span>
        <span className={styles.hint}>
          {!interactive || reducedMotion
            ? "A little freedom. A clear boundary."
            : !ready
              ? "Preparing your interactive scene…"
              : paused
                ? "A moment of stillness."
                : "Move your cursor to shape the terrain."}
        </span>
      </figcaption>
      {!reducedMotion && (
        <button
          type="button"
          className={styles.pauseButton}
          onClick={() => {
            if (!interactive) setInteractive(true);
            else setPaused((value) => !value);
          }}
          disabled={interactive && !ready}
          aria-label={
            !interactive
              ? "Explore in 3D"
              : !ready
                ? "Loading interactive scene"
                : paused
                  ? "Play Glassbound animation"
                  : "Pause Glassbound animation"
          }
          aria-pressed={interactive ? paused : undefined}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
          >
            {!interactive ? (
              <path
                d="m8 1 6 3.5v7L8 15l-6-3.5v-7L8 1Zm0 0v14M2 4.5 8 8l6-3.5"
                stroke="currentColor"
                strokeLinejoin="round"
              />
            ) : paused ? (
              <path d="m5 3 7 5-7 5V3Z" fill="currentColor" />
            ) : (
              <path
                d="M5 4v8M11 4v8"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            )}
          </svg>
          <span>
            {!interactive
              ? "Explore in 3D"
              : !ready
                ? "Preparing…"
                : paused
                  ? "Play"
                  : "Pause"}
          </span>
        </button>
      )}
      {interactive && (
        <button
          type="button"
          className={styles.resetButton}
          onClick={() => {
            setInteractive(false);
            setReady(false);
            setPaused(false);
          }}
        >
          Back to preview
        </button>
      )}
    </figure>
  );
}
