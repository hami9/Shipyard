import { useEffect, useState, type MouseEvent, type ReactNode } from "react";
import { href, parseRoute, type Route } from "./routes.ts";

// Client-side navigation over the History API, without a router library.

const event = "shipyard:navigate";

export function navigate(r: Route): void {
  window.history.pushState(null, "", href(r));
  window.dispatchEvent(new Event(event));
}

/** useRoute is the current page; it follows links and the back button. */
export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(window.location.pathname));
  useEffect(() => {
    const update = () => setRoute(parseRoute(window.location.pathname));
    window.addEventListener("popstate", update);
    window.addEventListener(event, update);
    return () => {
      window.removeEventListener("popstate", update);
      window.removeEventListener(event, update);
    };
  }, []);
  return route;
}

/** Link is an ordinary link that navigates in place; a modified click (new tab) is left to the browser. */
export function Link({ to, children, className }: { to: Route; children: ReactNode; className?: string }) {
  function click(e: MouseEvent<HTMLAnchorElement>) {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
      return;
    }
    e.preventDefault();
    navigate(to);
  }
  return (
    <a href={href(to)} onClick={click} className={className}>
      {children}
    </a>
  );
}
