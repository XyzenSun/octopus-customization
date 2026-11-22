using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@route("/api/v1/stats/today")
@get
@operationId("getStatsToday")
@tag("Stats")
@summary("获取日统计缓存")
@doc("返回 StatsDaily 对象, 包含 date 和平铺指标, 不是只有 StatsMetrics. 日期正常由服务器本地时间生成 YYYYMMDD. getter 不检查跨日, 午夜后若没有新的统计更新或缓存刷新, 可能仍返回前一天日期和数据. 正常无数据初始化为当天零指标对象. Source: internal/server/handlers/stats.go:38-39; internal/op/stats.go:231-246,353-356,436-447.")
op getStatsToday():
  | HttpResponse<200, Success<StatsDaily>, "日统计缓存对象.">
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/total")
@get
@operationId("getStatsTotal")
@tag("Stats")
@summary("获取全局累计统计")
@doc("返回 StatsTotal 内存缓存, 指标平铺并包含大写 JSON 字段 ID. 正常无记录初始化 ID 为 1, 不应把它写成 id 或只返回 StatsMetrics. Source: internal/server/handlers/stats.go:55-56; internal/model/stats.go:13-16; internal/op/stats.go:347-350,449-458.")
op getStatsTotal():
  | HttpResponse<200, Success<StatsTotal>, "全局累计统计对象.">
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/daily")
@get
@operationId("listStatsDaily")
@tag("Stats")
@summary("获取数据库全部日统计")
@doc("直接全表 Find, 无有效分页或时间过滤参数, 无显式排序, 不合并尚未落库的 today 缓存. 数据库查询错误返回 500. 空表时本项目把 nil slice 交给 GORM, 本次未读取对应依赖实现或运行验证, 最终空结果是 [] 还是 null 静态未确认, schema 保守允许两者. Source: internal/server/handlers/stats.go:42-48; internal/op/stats.go:427-433.")
op listStatsDaily():
  | HttpResponse<
      200,
      Success<ListStatsDailyStatus200application_jsonDataValue>,
      "数据库日统计列表. 空结果的 [] 或 null 表现静态未确认."
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/hourly")
@get
@operationId("listStatsHourly")
@tag("Stats")
@summary("获取今天截至当前小时的统计")
@doc("按服务器本地日期返回从 hour 0 到当前小时的有序数组, 无当日缓存的小时补零. 长度为当前小时加 1, 不是固定 24 项或过去 24 小时. 无请求参数过滤. 与 DBDump.stats_hourly 的滚动数据库表结果不同. Source: internal/server/handlers/stats.go:51-52; internal/op/stats.go:403-424.")
op listStatsHourly():
  | HttpResponse<
      200,
      Success<ListStatsHourlyStatus200application_jsonDataValue>,
      "今天截至当前小时的统计数组."
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/apikey")
@get
@operationId("listApiKeyStats")
@tag("Stats")
@summary("获取 API Key 维度统计缓存")
@doc("返回统计缓存已有条目, 不是 API Key 列表逐项补零. 无分页, 无排序保证, 空集合确定为 data: []. Source: internal/server/handlers/stats.go:59-60; internal/op/stats.go:395-400.")
op listApiKeyStats():
  | HttpResponse<200, Success<StatsAPIKey[]>, "API Key 统计数组.">
  | MissingAuth400
  | Unauthorized401
  | Internal500;

union ListStatsDailyStatus200application_jsonDataValue {
  StatsDaily[],
  null,
}

@minItems(1)
@maxItems(24)
model ListStatsHourlyStatus200application_jsonDataValue is StatsHourly[];
