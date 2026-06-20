from flask import Flask, Blueprint

main = Blueprint("main", __name__)
api = Blueprint("api", __name__)

app = Flask(__name__)
app.register_blueprint(main, url_prefix="/")
app.register_blueprint(api, url_prefix="/api")
