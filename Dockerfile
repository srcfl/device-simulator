FROM python:3.11-alpine
COPY . /app
WORKDIR /app
RUN pip install pymodbus==2.5.3
EXPOSE 502

ENTRYPOINT ["python"]
CMD ["modbus_server.py"]