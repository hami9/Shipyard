import logo from "./brand/logo.png";

/**
 * LogoTile is the logo on its white tile. It is decorative: the name
 * "Shipyard" always stands beside it, so it has no text of its own.
 */
export function LogoTile() {
  return (
    <span className="logo-tile" aria-hidden="true">
      <img src={logo} alt="" width={64} height={64} />
    </span>
  );
}
