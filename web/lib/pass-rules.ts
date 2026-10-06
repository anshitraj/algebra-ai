// How a Spend Pass asks for approval. approve_above_minor_units is nullable
// and zero is a real setting, so it can't be read as a plain truth value:
//
//   absent  never ask
//   0       ask before every call
//   n > 0   ask for calls at or above n
//
// Reading 0 as "no rule" would tell a person their pass never asks when it
// asks every time.

export type ApprovalRule = { kind: "never" } | { kind: "always" } | { kind: "above"; minor: number };

export function approvalRule(pass: { approve_above_minor_units?: number | null }): ApprovalRule {
  const above = pass.approve_above_minor_units;
  if (above === undefined || above === null) return { kind: "never" };
  if (above <= 0) return { kind: "always" };
  return { kind: "above", minor: above };
}
