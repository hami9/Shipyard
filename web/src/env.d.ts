// esbuild bundles imported stylesheets into the app's CSS file.
declare module "*.css";

// and copies imported images beside it, as their URL (scripts/build.mjs).
declare module "*.png" {
  const url: string;
  export default url;
}
