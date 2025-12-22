/**
 * Fastls gRPC Node.js客户端示例
 * 
 * 需要先安装依赖:
 * npm install @grpc/grpc-js @grpc/proto-loader
 */

const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');

// 加载proto文件
const PROTO_PATH = __dirname + '/../proto/fastls.proto';
const packageDefinition = protoLoader.loadSync(PROTO_PATH, {
    keepCase: true,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true
});

const fastlsProto = grpc.loadPackageDefinition(packageDefinition).fastls;

// 创建客户端
const client = new fastlsProto.FastlsService(
    'localhost:8802',
    grpc.credentials.createInsecure()
);

// 1. 健康检查
console.log('1. 健康检查:');
client.Health({}, (error, response) => {
    if (error) {
        console.error('   错误:', error.message);
    } else {
        console.log('   Status:', response.status);
    }
    console.log();
    
    // 2. 简单GET请求
    console.log('2. 简单GET请求:');
    client.Fetch({
        url: 'https://tls.peet.ws/api/all'
    }, (error, response) => {
        if (error) {
            console.error('   错误:', error.message);
        } else {
            console.log('   Status:', response.status);
            console.log('   OK:', response.ok);
            console.log('   Body length:', response.body ? response.body.length : 0, 'bytes');
        }
        console.log();
        
        // 3. 使用浏览器指纹
        console.log('3. 使用Chrome142指纹:');
        client.Fetch({
            url: 'https://tls.peet.ws/api/all',
            browser: 'chrome142'
        }, (error, response) => {
            if (error) {
                console.error('   错误:', error.message);
            } else {
                console.log('   Status:', response.status);
                console.log('   OK:', response.ok);
            }
            console.log();
            
            // 4. 使用自定义JA3指纹
            console.log('4. 使用自定义JA3指纹:');
            client.Fetch({
                url: 'https://tls.peet.ws/api/all',
                fingerprint: {
                    type: 'ja3',
                    value: '771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0'
                }
            }, (error, response) => {
                if (error) {
                    console.error('   错误:', error.message);
                } else {
                    console.log('   Status:', response.status);
                    console.log('   OK:', response.ok);
                }
                console.log();
                
                // 5. POST请求
                console.log('5. POST请求:');
                client.Fetch({
                    url: 'https://httpbin.org/post',
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify({ key: 'value' })
                }, (error, response) => {
                    if (error) {
                        console.error('   错误:', error.message);
                    } else {
                        console.log('   Status:', response.status);
                        console.log('   OK:', response.ok);
                    }
                });
            });
        });
    });
});

