/** Shared contract between the onboarding tour and the grouped admin sidebar. */
export const SIDEBAR_REVEAL_EVENT = 'sub2api:sidebar-reveal'

const tourAnchors: Record<string, string> = {
  '/admin/accounts': 'sidebar-channel-manage',
  '/admin/groups': 'sidebar-group-manage',
  '/admin/redeem': 'sidebar-wallet'
}

export function sidebarTourAnchor(path: string): string | undefined {
  return tourAnchors[path]
}

export function requestSidebarReveal(selector: string): void {
  if (Object.values(tourAnchors).some(anchor => selector === `#${anchor}`)) {
    window.dispatchEvent(new CustomEvent(SIDEBAR_REVEAL_EVENT, { detail: selector }))
  }
}
