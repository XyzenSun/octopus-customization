import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";

const createNextConfig = (phase: string): NextConfig => {
  const isDev = phase === PHASE_DEVELOPMENT_SERVER;
  if (!isDev) {
    return {
      reactCompiler: true,
      output: "export",
      assetPrefix: "./",
    };
  }

  const backendTarget = (
    process.env.OCTOPUS_DEV_PROXY_TARGET ?? "http://127.0.0.1:8080"
  ).replace(/\/+$/, "");

  return {
    reactCompiler: true,
    // 浏览器始终访问 Next dev server，由 rewrite 转发到本地或远程后端，
    // 从而保持管理员 Cookie 同源；生产静态导出不会包含这些 rewrites。
    rewrites: async () => [
      {
        source: "/api/:path*",
        destination: `${backendTarget}/api/:path*`,
      },
      {
        source: "/v1/:path*",
        destination: `${backendTarget}/v1/:path*`,
      },
    ],
  };
};

export default createNextConfig;
