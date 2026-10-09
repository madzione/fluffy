import 'dart:typed_data';

/// Sends one message to the other side: its JSON envelope and the byte data
/// the envelope's `bin` paths refer to.
typedef Send = void Function(Uint8List json, List<Uint8List> blobs);

// A frame is, little endian: uint32 length of the JSON envelope, uint32
// number of blobs, the envelope, then per blob its uint32 length and bytes.
// golang/fluffy/bridge.go reads and writes the same.

/// The size of the frame of a message.
int frameSize(Uint8List json, List<Uint8List> blobs) {
  var size = 8 + json.length;
  for (final blob in blobs) {
    size += 4 + blob.length;
  }
  return size;
}

/// Writes the frame of a message into [target], which has [frameSize] bytes.
void writeFrame(Uint8List target, Uint8List json, List<Uint8List> blobs) {
  final view = ByteData.sublistView(target)
    ..setUint32(0, json.length, Endian.little)
    ..setUint32(4, blobs.length, Endian.little);
  target.setRange(8, 8 + json.length, json);
  var offset = 8 + json.length;
  for (final blob in blobs) {
    view.setUint32(offset, blob.length, Endian.little);
    target.setRange(offset + 4, offset + 4 + blob.length, blob);
    offset += 4 + blob.length;
  }
}
