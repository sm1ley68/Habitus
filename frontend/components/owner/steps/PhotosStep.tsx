"use client";
import PhotoUploader from "../PhotoUploader";

export default function PhotosStep({
  listingId, photos, onChange,
}: { listingId: string | null; photos: string[]; onChange: (photos: string[]) => void }) {
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-[15px] tracking-tight text-[#1c1d20]">Фотографии</h2>
        <p className="mt-1 text-sm text-zinc-500">
          Первая станет обложкой в выдаче. Шаг можно пропустить и вернуться к нему позже.
        </p>
      </div>

      {listingId ? (
        <PhotoUploader listingId={listingId} photos={photos} onChange={onChange} />
      ) : (
        <p className="text-sm text-zinc-400">
          Черновик ещё создаётся — фотографии можно будет добавить через секунду.
        </p>
      )}
    </div>
  );
}
