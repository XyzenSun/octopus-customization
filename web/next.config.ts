import type { NextConfig } from "next";
import { PHASE_DEVELOPMENT_SERVER } from "next/constants";

const createNextConfig = (phase: string): NextConfig => {
  const isDev = phase === PHASE_DEVELOPMENT_SERVER;
  return {
    reactCompiler: true,
    // dev 模式下不用静态导出，以便使用 rewrites 代理 API 绕过跨域
    ...(isDev ? {} : { output: "export" }),
    ...(isDev ? {} : { assetPrefix: "./" }),
    ...(isDev ? {
      rewrites: async () => [
        {
          source: "/api/:path*",
          destination: "http://127.0.0.1:8080/api/:path*",
        },
        {
          source: "/v1/:path*",
          destination: "http://127.0.0.1:8080/v1/:path*",
        },
      ],
    } : {}),
  };
};

export default createNextConfig;

