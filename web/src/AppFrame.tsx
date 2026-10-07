import type { ReactNode } from "react";
import type { Scope } from "./api/schema.ts";
import { canChange } from "./forms.ts";
import { Link } from "./nav.tsx";

export type Section = "app" | "env" | "domains" | "logs" | "settings";

const sections: { page: Section; label: string }[] = [
  { page: "app", label: "Overview" },
  { page: "env", label: "Environment" },
  { page: "domains", label: "Domains" },
  { page: "logs", label: "Logs" },
  { page: "settings", label: "Settings" },
];

interface Props {
  slug: string;
  section: Section;
  scopes: Scope[];
  /** Beside the heading: a revision, a status. */
  aside?: ReactNode;
  children: ReactNode;
}

/**
 * AppFrame is what every page of one app shares: where it is, its heading,
 * and tabs to its other pages, so moving between them takes one click. The
 * heading names the page: the app on its overview, the section elsewhere.
 * Settings is listed only for a token that may change them.
 */
export function AppFrame({ slug, section, scopes, aside, children }: Props) {
  const here = sections.find((s) => s.page === section)?.label ?? "";
  const shown = sections.filter((s) => s.page !== "settings" || canChange(scopes));
  return (
    <section>
      <p className="crumbs">
        <Link to={{ page: "apps" }}>Apps</Link>
        {" / "}
        {section === "app" ? slug : <Link to={{ page: "app", slug }}>{slug}</Link>}
        {section !== "app" && ` / ${here}`}
      </p>
      <div className="title">
        <h1>{section === "app" ? slug : here}</h1>
        {aside}
      </div>
      <nav className="tabs" aria-label={`${slug} pages`}>
        {shown.map((s) => (
          <Link key={s.page} to={{ page: s.page, slug }} current={s.page === section}>
            {s.label}
          </Link>
        ))}
      </nav>
      {children}
    </section>
  );
}
