declare module "ui-kit" {
  export const theme: any;
  export const Logo: React.FC<{ collapsed?: boolean }>;
}

declare module "*.png" {
  const value: any;
  export default value;
}
