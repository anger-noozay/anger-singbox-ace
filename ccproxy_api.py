#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import configparser
from datetime import datetime
import os
import sys
from flask import Flask, request, jsonify

app = Flask(__name__)

# 全局配置文件路径
CONFIG_FILE = None
API_PORT = 5000  # 默认端口

def get_user_expiry(username):
    """根据用户名查询到期时间"""
    global CONFIG_FILE
    
    if not CONFIG_FILE or not os.path.exists(CONFIG_FILE):
        return None, "配置文件不存在，请先设置正确的 AccInfo.ini 路径"
    
    try:
        config = configparser.ConfigParser()
        config.read(CONFIG_FILE, encoding='utf-8')
        
        for section in config.sections():
            if section.startswith('User') and section[4:].isdigit():
                if config.get(section, 'UserName', fallback='') == username:
                    disable_date_time = config.get(section, 'DisableDateTime', fallback='')
                    enabled = config.getboolean(section, 'Enable', fallback=False)
                    
                    if not enabled:
                        return None, "用户已禁用"
                    
                    if not disable_date_time:
                        return None, "未设置到期时间"
                    
                    now = datetime.now()
                    expire_time = datetime.strptime(disable_date_time, '%Y-%m-%d %H:%M:%S')
                    is_expired = expire_time < now
                    remaining_days = (expire_time - now).days if not is_expired else 0
                    
                    return {
                        'expire_time': disable_date_time,
                        'remaining_days': remaining_days,
                        'is_expired': is_expired
                    }, None
        
        return None, f"用户 {username} 不存在"
    
    except Exception as e:
        return None, f"解析配置文件出错: {str(e)}"

@app.route('/api/user/expiry', methods=['GET', 'POST'])
def query_expiry():
    """查询用户到期时间 API"""
    if request.method == 'GET':
        username = request.args.get('username')
    else:
        username = request.json.get('username') if request.is_json else request.form.get('username')
    
    if not username:
        return jsonify({'code': -1, 'msg': '用户名不能为空'})
    
    result, error = get_user_expiry(username)
    
    if error:
        return jsonify({'code': -1, 'msg': error})
    
    return jsonify({
        'code': 1,
        'data': result
    })

@app.route('/api/health', methods=['GET'])
def health_check():
    """健康检查接口"""
    return jsonify({
        'code': 1,
        'msg': 'API服务正常运行',
        'config_file': CONFIG_FILE,
        'port': API_PORT
    })

def get_port_from_user():
    """获取用户输入的端口号"""
    global API_PORT
    
    while True:
        port_input = input(f"请输入 API 端口号（默认 {API_PORT}）: ").strip()
        
        # 如果直接回车，使用默认端口
        if port_input == "":
            print(f"✅ 使用默认端口: {API_PORT}")
            return API_PORT
        
        # 验证端口号是否合法
        try:
            port = int(port_input)
            if 1 <= port <= 65535:
                # 检查端口是否被占用（可选）
                API_PORT = port
                print(f"✅ 使用自定义端口: {API_PORT}")
                return API_PORT
            else:
                print("❌ 端口号范围应为 1-65535，请重新输入\n")
        except ValueError:
            print("❌ 请输入有效的数字端口号\n")

def get_config_path_from_user():
    """获取用户输入的配置文件路径"""
    global CONFIG_FILE
    
    print()
    print("=" * 50)
    print("📁 请指定 CCProxy 用户配置文件")
    print("=" * 50)
    print("提示: AccInfo.ini 通常位于 CCProxy 安装目录下")
    print("     常见路径: C:\\CCProxy\\AccInfo.ini 或 D:\\CCProxy\\AccInfo.ini")
    print()
    
    while True:
        path = input("请输入 AccInfo.ini 完整路径（或所在目录）: ").strip()
        
        # 去掉可能的引号
        path = path.strip('"').strip("'")
        
        # 如果只输入了目录，自动补全文件名
        if os.path.isdir(path):
            test_path = os.path.join(path, 'AccInfo.ini')
            if os.path.exists(test_path):
                CONFIG_FILE = test_path
                break
            else:
                print(f"❌ 目录下未找到 AccInfo.ini 文件: {test_path}")
                print("   请确认路径是否正确\n")
                continue
        elif os.path.isfile(path):
            if path.endswith('AccInfo.ini'):
                CONFIG_FILE = path
                break
            else:
                print("❌ 文件不是 AccInfo.ini，请选择正确的配置文件\n")
                continue
        else:
            print("❌ 路径不存在，请重新输入\n")
            continue
    
    print()
    print(f"✅ 配置文件加载成功: {CONFIG_FILE}")
    
    # 验证配置文件是否能正常读取
    try:
        config = configparser.ConfigParser()
        config.read(CONFIG_FILE, encoding='utf-8')
        user_count = 0
        for section in config.sections():
            if section.startswith('User') and section[4:].isdigit():
                user_count += 1
        print(f"✅ 检测到 {user_count} 个用户配置")
    except Exception as e:
        print(f"⚠️ 配置文件读取警告: {e}")

def main():
    global API_PORT, CONFIG_FILE
    
    print()
    print("=" * 50)
    print("🔧 CCProxy 用户到期时间查询 API")
    print("=" * 50)
    
    # 获取配置文件路径
    get_config_path_from_user()
    
    # 获取端口号
    print()
    get_port_from_user()
    
    # 启动信息
    print()
    print("=" * 50)
    print("🚀 启动 API 服务...")
    print("=" * 50)
    print()
    print(f"📡 API 地址:")
    print(f"   GET 查询: http://127.0.0.1:{API_PORT}/api/user/expiry?username=用户名")
    print(f"   POST 查询: http://127.0.0.1:{API_PORT}/api/user/expiry")
    print(f"   健康检查: http://127.0.0.1:{API_PORT}/api/health")
    print()
    print("📝 使用示例:")
    print(f"   curl \"http://127.0.0.1:{API_PORT}/api/user/expiry?username=1\"")
    print()
    print("💡 按 Ctrl+C 停止服务")
    print("=" * 50)
    print()
    
    # 启动 Flask 服务
    try:
        app.run(host='0.0.0.0', port=API_PORT, debug=False, use_reloader=False)
    except OSError as e:
        if "Address already in use" in str(e):
            print(f"\n❌ 端口 {API_PORT} 已被占用，请重新运行并选择其他端口")
        else:
            print(f"\n❌ 启动失败: {e}")
        sys.exit(1)

if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        print("\n\n👋 服务已停止")
        sys.exit(0)
    except Exception as e:
        print(f"\n❌ 启动失败: {e}")
        sys.exit(1)