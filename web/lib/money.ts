// Amounts are minor units of their own currency: paise for INR, millionths
// for USDC. Never a float on the wire; this is only how they are shown.

const USDC_UNIT = 1_000_000;

/** The number part of a USDC amount: "0.05", "1.00", "0.0015". */
function usdcNumber(minor: number, minFraction = 2): string {
  return (minor / USDC_UNIT).toLocaleString("en-US", { minimumFractionDigits: minFraction, maximumFractionDigits: 6 });
}

/** "0.05 USDC". */
export function formatUSDC(minor: number): string {
  return `${usdcNumber(minor)} USDC`;
}

/** An amount in its currency: "0.05 USDC" or "₹500". */
export function formatMoney(minor: number, currency: string): string {
  return currency === "USDC" ? formatUSDC(minor) : `₹${(minor / 100).toLocaleString("en-IN", { maximumFractionDigits: 0 })}`;
}

/**
 * A price range as Pay.sh lists it: "listed free", "0.0015 USDC" or
 * "0.001–1 USDC". These are listings; the real price is whatever the endpoint
 * asks for when Algebra requests a quote.
 */
export function formatPriceRange(minMinor: number, maxMinor: number): string {
  if (maxMinor <= 0) return "listed free";
  if (minMinor === maxMinor) return formatUSDC(maxMinor);
  return `${usdcNumber(minMinor, 0)}–${usdcNumber(maxMinor, 0)} USDC`;
}

/** "0.003 USDC" for a priced endpoint, or "listed free". */
export function formatEndpointPrice(priceMinor: number, free: boolean): string {
  return free || priceMinor <= 0 ? "listed free" : formatUSDC(priceMinor);
}
