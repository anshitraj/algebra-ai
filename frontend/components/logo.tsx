"use client";

import { motion } from "motion/react";

/**
 * The Gated Equals mark: an "=" where one bar is broken by a checkpoint
 * node — every transaction passes through a gate. The node closes the gap
 * on hover/focus, the one authored micro-interaction this mark carries
 * wherever it appears (nav, footer, favicon source).
 */
export function Logo({
  size = 32,
  className = "",
}: {
  size?: number;
  className?: string;
}) {
  return (
    <motion.svg
      viewBox="0 0 40 40"
      width={size}
      height={size}
      className={className}
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      role="img"
      aria-label="Algebra"
      initial="rest"
      whileHover="gated"
      whileFocus="gated"
    >
      <line
        x1="7"
        y1="26"
        x2="33"
        y2="26"
        stroke="currentColor"
        strokeWidth="3.75"
        strokeLinecap="round"
      />
      <motion.line
        x1="7"
        y1="15"
        x2="15.5"
        y2="15"
        stroke="currentColor"
        strokeWidth="3.75"
        strokeLinecap="round"
        variants={{ rest: { x2: 15.5 }, gated: { x2: 17 } }}
        transition={{ type: "spring", stiffness: 420, damping: 24 }}
      />
      <motion.line
        x1="24.5"
        y1="15"
        x2="33"
        y2="15"
        stroke="currentColor"
        strokeWidth="3.75"
        strokeLinecap="round"
        variants={{ rest: { x1: 24.5 }, gated: { x1: 23 } }}
        transition={{ type: "spring", stiffness: 420, damping: 24 }}
      />
      <motion.circle
        cx="20"
        cy="15"
        r="4.25"
        fill="currentColor"
        variants={{ rest: { scale: 1 }, gated: { scale: 1.2 } }}
        transition={{ type: "spring", stiffness: 420, damping: 18 }}
      />
    </motion.svg>
  );
}
