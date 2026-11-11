curl -X POST http://localhost:8080/api/v1/builds \
  -F 'file=@culina.zip' \
  -F 'icon=@app.png' \
  -F 'config={"appName":"CULINA","appID":"com.trevor.culina","version":"1.0.0","description":"Cooking chef application ","platform":"android","frontend":{"framework":"auto","packageManager":"npm"},"window":{"title":"CULINA","width":1200,"height":800,"resizable":true}}