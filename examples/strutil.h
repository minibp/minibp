#ifndef STRUTIL_H
#define STRUTIL_H

#include <string>
#include <vector>

namespace strutil {

// 将字符串转换为大写
std::string to_upper(const std::string& str);

// 将字符串转换为小写
std::string to_lower(const std::string& str);

// 去除字符串两端的空白字符
std::string trim(const std::string& str);

// 连接字符串数组
std::string join(const std::string& separator, const std::vector<std::string>& parts);

} // namespace strutil

#endif // STRUTIL_H
