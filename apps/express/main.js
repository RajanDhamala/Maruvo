import grpc from "@grpc/grpc-js"
import protoLoader from "@grpc/proto-loader"

const packageDefinition = protoLoader.loadSync("./proto/hello.proto", {
  keepCase: true,
  longs: String,
  enums: String,
  defaults: true,
})

const proto = grpc.loadPackageDefinition(packageDefinition).rpc

const sayHello = (call, callback) => {
  const { name, age } = call.request

  console.log("Received:", name, age)

  callback(null, {
    isEligible: age >= 18,
  })
}

const grpcServer = new grpc.Server()

grpcServer.addService(proto.HelloService.service, {
  sayHello,
})

grpcServer.bindAsync(
  "127.0.0.1:50051",
  grpc.ServerCredentials.createInsecure(),
  (err, port) => {
    if (err) {
      console.error(err)
      return
    }

    console.log(`gRPC listening on port ${port}`)
  }
)
