"use client";

import dynamic from "next/dynamic";
import { useEffect, useRef, useState } from "react";
import { useReducedMotion } from "motion/react";
import styles from "./glassbound-accent.module.css";

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
  quality: 65,
  missingTiles: 18,
  hover: 38,
};

export function GlassboundAccent() {
  const ref = useRef<HTMLElement>(null);
  const [load, setLoad] = useState(false);
  const [paused, setPaused] = useState(false);
  const reducedMotion = useReducedMotion();

  useEffect(() => {
    const element = ref.current;
    if (!element) return;
    let started = false;
    const prepare = () => {
      if (started) return;
      started = true;
      setLoad(true);
      observer.disconnect();
    };
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) prepare();
      },
      { rootMargin: "1200px" },
    );
    observer.observe(element);
    // Prepare the shader during idle time, before a fast scroll reaches it.
    // The scene itself only draws while visible.
    const idle = "requestIdleCallback" in window
      ? window.requestIdleCallback(prepare, { timeout: 1500 })
      : null;
    const timer = idle === null ? setTimeout(prepare, 500) : null;
    return () => {
      observer.disconnect();
      if (idle !== null) window.cancelIdleCallback(idle);
      if (timer !== null) clearTimeout(timer);
    };
  }, []);

  return (
    <figure
      ref={ref}
      className={styles.figure}
      aria-label="Glassbound illustration"
    >
      <div className={styles.fallback} aria-hidden="true">
        <span className={styles.fallbackGrid} />
        <span className={styles.fallbackBall} />
      </div>
      <div className={styles.stage}>
        {load && (
          <Scene
            background="#f8f8f3"
            accent="#86956b"
            speed={24}
            size={165}
            radius={3.4}
            lift={72}
            advanced={ADVANCED}
            paused={paused || Boolean(reducedMotion)}
          />
        )}
      </div>
      <figcaption className={styles.caption}>
        <span>
          GLASSBOUND <i>/</i> ORIGINKIT
        </span>
        <span className={styles.hint}>
          {reducedMotion
            ? "A little freedom. A clear boundary."
            : paused
              ? "A moment of stillness."
              : "Hover or touch to shape the terrain."}
        </span>
      </figcaption>
      <button
        type="button"
        className={styles.pauseButton}
        disabled={Boolean(reducedMotion)}
        onClick={() => setPaused((value) => !value)}
        aria-label={
          reducedMotion
            ? "Animation disabled for reduced motion"
            : paused
              ? "Play Glassbound animation"
              : "Pause Glassbound animation"
        }
        aria-pressed={paused || Boolean(reducedMotion)}
      >
        <svg
          width="12"
          height="12"
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          {paused || reducedMotion ? (
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
        <span>{reducedMotion ? "Still" : paused ? "Play" : "Pause"}</span>
      </button>
    </figure>
  );
}
