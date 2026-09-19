/**
 * Detect whether the current device is running Vivo's OriginOS.
 *
 * OriginOS advertises itself through a brand substring that overlaps with
 * FunTouchOS / Ocean, so the check has to look at several navigator hints at
 * once. Used to decide whether to surface the battery whitelist / autostart
 * onboarding flow for the flashcard daily-review reminder.
 */
export function isVivoOriginOS(): boolean {
  if (typeof navigator === 'undefined') return false;

  const ua = navigator.userAgent || '';
  const platform = (navigator as Navigator & { vendor?: string }).vendor || '';
  const blob = [
    navigator.platform || '',
    ua,
    platform,
  ].join(' ').toLowerCase();

  const isVivo = /vivo|v1[12]i|v\d{4}|iqoo/.test(blob);
  const isAndroid = /android/.test(blob);
  const originOsMarker = /origin\s?os|originos|funtouch/.test(blob);

  return isVivo && isAndroid && originOsMarker;
}
